package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"umpp/control-plane/internal/models"
	"umpp/control-plane/internal/service/cert"
	acmeclient "umpp/control-plane/internal/service/cert/acme"
	"umpp/control-plane/internal/validation"
)

const (
	CertificateStatusPending = "pending"
	CertificateStatusActive  = "active"
	CertificateStatusFailed  = "failed"
	CertificateStatusRevoked = "revoked"
)

var ErrOrderInFlight = errors.New("certificate order already in flight")

type CertificateMetrics interface {
	ObserveACMEOrder(result string, duration time.Duration)
	ObserveCertificateRenewal(result string)
	SetCertificateExpiry(domain string, days float64)
}

type CertificateConfigBumper interface {
	BumpForProxy(ctx context.Context, proxyRuleID uuid.UUID, reason string) (models.ConfigVersion, error)
	BumpForNode(ctx context.Context, nodeID uuid.UUID, reason string) (models.ConfigVersion, error)
}

type CertificateCacheInvalidator interface {
	InvalidateCertificate(domain string)
}

type ACMEOptions struct {
	AutoRenew       bool
	RenewBefore     time.Duration
	CheckInterval   time.Duration
	ChallengeSolver acmeclient.ChallengeSolver
}

type CertificateService struct {
	db          *gorm.DB
	crypto      *cert.Crypto
	acme        acmeclient.Client
	options     ACMEOptions
	metrics     CertificateMetrics
	bumper      CertificateConfigBumper
	invalidator CertificateCacheInvalidator
	logger      *slog.Logger
	workerCtx   context.Context

	flightMu sync.Mutex
	flights  map[uuid.UUID]struct{}
}

type CertificateInput struct {
	Issuer  string `json:"issuer,omitempty"`
	Domain  string `json:"domain,omitempty"`
	CertPEM string `json:"cert_pem,omitempty"`
	KeyPEM  string `json:"key_pem,omitempty"`
}

type CertificateList struct {
	Items    []models.Certificate `json:"items"`
	Total    int64                `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
}

func NewCertificateService(db *gorm.DB, crypto *cert.Crypto) *CertificateService {
	return &CertificateService{
		db:      db,
		crypto:  crypto,
		logger:  slog.Default(),
		flights: make(map[uuid.UUID]struct{}),
	}
}

func (s *CertificateService) ConfigureACME(client acmeclient.Client, options ACMEOptions, metrics CertificateMetrics, bumper CertificateConfigBumper, invalidator CertificateCacheInvalidator, logger *slog.Logger) {
	s.acme = client
	s.options = options
	s.metrics = metrics
	s.bumper = bumper
	s.invalidator = invalidator
	if logger != nil {
		s.logger = logger
	}
}

func (s *CertificateService) List(ctx context.Context, tenantID *uuid.UUID, domain string, page, pageSize int) (CertificateList, error) {
	query := s.db.WithContext(ctx).Model(&models.Certificate{})
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	if domain != "" {
		query = query.Where("domain = ?", strings.ToLower(domain))
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return CertificateList{}, fmt.Errorf("count certificates: %w", err)
	}
	var items []models.Certificate
	if err := query.Order("expires_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error; err != nil {
		return CertificateList{}, fmt.Errorf("list certificates: %w", err)
	}
	return CertificateList{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (s *CertificateService) Get(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) (models.Certificate, error) {
	query := s.db.WithContext(ctx).Where("id = ?", id)
	if tenantID != nil {
		query = query.Where("tenant_id = ?", *tenantID)
	}
	var item models.Certificate
	err := query.First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Certificate{}, ErrNotFound
	}
	if err != nil {
		return models.Certificate{}, fmt.Errorf("get certificate: %w", err)
	}
	return item, nil
}

func (s *CertificateService) Import(ctx context.Context, tenantID uuid.UUID, input CertificateInput) (models.Certificate, error) {
	leaf, err := cert.ParseAndMatch(input.CertPEM, input.KeyPEM)
	if err != nil {
		return models.Certificate{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	domain, err := certificateDomain(leaf.DNSNames, leaf.Subject.CommonName)
	if err != nil {
		return models.Certificate{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	encrypted, err := s.crypto.Encrypt(strings.TrimSpace(input.KeyPEM))
	if err != nil {
		return models.Certificate{}, err
	}
	expiresAt := leaf.NotAfter.UTC()
	item := models.Certificate{
		ID:        uuid.New(),
		TenantID:  tenantID,
		Domain:    domain,
		Issuer:    leaf.Issuer.String(),
		CertPEM:   strings.TrimSpace(input.CertPEM),
		KeyPEM:    encrypted,
		ExpiresAt: &expiresAt,
		Status:    CertificateStatusActive,
		AutoRenew: false,
		CreatedAt: time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Create(&item).Error; err != nil {
		return models.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}
	if s.invalidator != nil {
		s.invalidator.InvalidateCertificate(domain)
	}
	return item, nil
}

// RequestACME creates a pending record and queues an asynchronous order.
func (s *CertificateService) RequestACME(ctx context.Context, tenantID uuid.UUID, input CertificateInput) (models.Certificate, error) {
	if s.acme == nil {
		return models.Certificate{}, fmt.Errorf("%w: ACME is not configured", ErrInvalidInput)
	}
	if err := s.acme.Validate(acmeclient.ChallengeHTTP01); err != nil {
		return models.Certificate{}, err
	}
	domain, err := validation.Domain(strings.ToLower(strings.TrimSpace(input.Domain)))
	if err != nil {
		return models.Certificate{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	item := models.Certificate{
		ID:            uuid.New(),
		TenantID:      tenantID,
		Domain:        domain,
		Issuer:        acmeclient.Issuer,
		Status:        CertificateStatusPending,
		ChallengeType: acmeclient.ChallengeHTTP01,
		AutoRenew:     s.options.AutoRenew,
		CreatedAt:     time.Now().UTC(),
	}
	if err := s.db.WithContext(ctx).Create(&item).Error; err != nil {
		return models.Certificate{}, fmt.Errorf("create pending ACME certificate: %w", err)
	}
	if err := s.startFlight(item.ID); err != nil {
		return models.Certificate{}, err
	}
	return item, nil
}

// Renew queues one renewal/issuance order. A second request while that order is
// running receives ErrOrderInFlight, so a certificate can have at most one live
// ACME order.
func (s *CertificateService) Renew(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) (models.Certificate, error) {
	item, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return models.Certificate{}, err
	}
	if item.Issuer != acmeclient.Issuer {
		return models.Certificate{}, fmt.Errorf("%w: only ACME certificates can be renewed", ErrInvalidInput)
	}
	if err := s.startFlight(id); err != nil {
		return models.Certificate{}, err
	}
	return item, nil
}

func (s *CertificateService) Revoke(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) error {
	item, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return err
	}
	if s.acme == nil {
		return acmeclient.ErrNotImplemented
	}
	return s.acme.Revoke(ctx, []byte(item.CertPEM))
}

func (s *CertificateService) Delete(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) error {
	item, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return err
	}
	var references int64
	if err := s.db.WithContext(ctx).Model(&models.Domain{}).Where("cert_id = ?", item.ID).Count(&references).Error; err != nil {
		return fmt.Errorf("check certificate domain references: %w", err)
	}
	if references > 0 {
		return fmt.Errorf("%w: certificate is referenced by %d domain(s)", ErrConflict, references)
	}
	if err := s.db.WithContext(ctx).Delete(&models.Certificate{}, "id = ?", item.ID).Error; err != nil {
		return fmt.Errorf("delete certificate: %w", err)
	}
	return nil
}

func (s *CertificateService) startFlight(id uuid.UUID) error {
	workerCtx := s.workerCtx
	if workerCtx == nil {
		workerCtx = context.Background()
	}
	s.flightMu.Lock()
	if _, ok := s.flights[id]; ok {
		s.flightMu.Unlock()
		return ErrOrderInFlight
	}
	s.flights[id] = struct{}{}
	s.flightMu.Unlock()
	go s.processOrder(workerCtx, id)
	return nil
}

func (s *CertificateService) processOrder(parent context.Context, id uuid.UUID) {
	defer func() {
		s.flightMu.Lock()
		delete(s.flights, id)
		s.flightMu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	item, err := s.Get(ctx, id, nil)
	if err != nil {
		s.logger.Error("load certificate for ACME order", "certificate_id", id, "error", err)
		return
	}
	renewal := item.CertPEM != "" || item.Status == CertificateStatusActive
	started := time.Now()
	certPEM, keyPEM, notAfter, orderErr := s.acme.Obtain(ctx, item.Domain, s.options.ChallengeSolver)
	duration := time.Since(started)
	result := "success"
	if orderErr != nil {
		result = "failure"
	}
	if s.metrics != nil {
		s.metrics.ObserveACMEOrder(result, duration)
	}
	if orderErr != nil {
		s.recordOrderFailure(ctx, item, renewal, orderErr)
		return
	}
	encrypted, err := s.crypto.Encrypt(string(keyPEM))
	if err != nil {
		s.recordOrderFailure(ctx, item, renewal, fmt.Errorf("encrypt issued private key: %w", err))
		return
	}
	if _, err := cert.ParseAndMatch(string(certPEM), string(keyPEM)); err != nil {
		s.recordOrderFailure(ctx, item, renewal, fmt.Errorf("validate issued certificate: %w", err))
		return
	}
	now := time.Now().UTC()
	expires := notAfter.UTC()
	renewedAt := item.RenewedAt
	renewCount := item.RenewCount
	if renewal {
		renewedAt = &now
		renewCount++
	}
	updates := map[string]any{
		"cert_pem":         strings.TrimSpace(string(certPEM)),
		"key_pem":          encrypted,
		"expires_at":       expires,
		"status":           CertificateStatusActive,
		"last_error":       "",
		"renewed_at":       renewedAt,
		"renew_count":      renewCount,
		"challenge_type":   acmeclient.ChallengeHTTP01,
		"next_attempt_at":  nil,
		"renewal_failures": 0,
	}
	if err := s.db.WithContext(ctx).Model(&models.Certificate{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		s.logger.Error("store issued certificate", "certificate_id", id, "error", err)
		if s.metrics != nil {
			s.metrics.ObserveCertificateRenewal("failure")
		}
		return
	}
	if renewal && s.metrics != nil {
		s.metrics.ObserveCertificateRenewal("success")
	}
	if s.metrics != nil {
		s.metrics.SetCertificateExpiry(item.Domain, time.Until(expires).Hours()/24)
	}
	if s.invalidator != nil {
		s.invalidator.InvalidateCertificate(item.Domain)
	}
	s.bumpCertificateConfig(ctx, item.ID, "ACME certificate renewed")
}

func (s *CertificateService) recordOrderFailure(ctx context.Context, item models.Certificate, renewal bool, orderErr error) {
	now := time.Now().UTC()
	failures := item.RenewalFailures + 1
	nextAttempt := now.Add(certificateBackoff(failures))
	status := item.Status
	if !renewal {
		status = CertificateStatusFailed
	}
	updates := map[string]any{
		"status":           status,
		"last_error":       truncateError(orderErr.Error()),
		"next_attempt_at":  nextAttempt,
		"renewal_failures": failures,
	}
	if err := s.db.WithContext(ctx).Model(&models.Certificate{}).Where("id = ?", item.ID).Updates(updates).Error; err != nil {
		s.logger.Error("record ACME order failure", "certificate_id", item.ID, "error", err)
	}
	if renewal && s.metrics != nil {
		s.metrics.ObserveCertificateRenewal("failure")
	}
	s.logger.Warn("ACME certificate order failed", "certificate_id", item.ID, "domain", item.Domain, "renewal", renewal, "next_attempt_at", nextAttempt, "error", orderErr)
}

func (s *CertificateService) bumpCertificateConfig(ctx context.Context, certificateID uuid.UUID, reason string) {
	if s.bumper == nil {
		return
	}
	var domainIDs []uuid.UUID
	if err := s.db.WithContext(ctx).Model(&models.Domain{}).Where("cert_id = ?", certificateID).Pluck("id", &domainIDs).Error; err != nil {
		s.logger.Error("load domains for certificate config bump", "certificate_id", certificateID, "error", err)
		return
	}
	var proxyRuleIDs []uuid.UUID
	if len(domainIDs) > 0 {
		if err := s.db.WithContext(ctx).Model(&models.ProxyRule{}).Where("domain_id IN ?", domainIDs).Pluck("id", &proxyRuleIDs).Error; err != nil {
			s.logger.Error("load proxy rules for certificate config bump", "certificate_id", certificateID, "error", err)
		}
	}
	for _, proxyRuleID := range proxyRuleIDs {
		if _, err := s.bumper.BumpForProxy(ctx, proxyRuleID, reason); err != nil {
			s.logger.Error("bump proxy config after certificate renewal", "proxy_rule_id", proxyRuleID, "error", err)
		}
	}
	var tenantIDs []uuid.UUID
	if err := s.db.WithContext(ctx).Model(&models.Certificate{}).Where("id = ?", certificateID).Pluck("tenant_id", &tenantIDs).Error; err == nil && len(tenantIDs) == 1 {
		tenantID := tenantIDs[0]
		var nodeIDs []uuid.UUID
		if err := s.db.WithContext(ctx).Model(&models.Node{}).Where("tenant_id = ?", tenantID).Pluck("id", &nodeIDs).Error; err == nil {
			for _, nodeID := range nodeIDs {
				if _, err := s.bumper.BumpForNode(ctx, nodeID, reason); err != nil {
					s.logger.Error("bump node config after certificate renewal", "node_id", nodeID, "error", err)
				}
			}
		}
	}
}

// Start resumes pending work and runs the automatic renewal scanner until the
// context is canceled.
func (s *CertificateService) Start(ctx context.Context) {
	s.workerCtx = ctx
	s.resumePending(ctx)
	s.refreshExpiryMetrics(ctx)
	interval := s.options.CheckInterval
	if interval <= 0 {
		interval = 6 * time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshExpiryMetrics(ctx)
			s.renewDue(ctx)
		}
	}
}

func (s *CertificateService) resumePending(ctx context.Context) {
	var ids []uuid.UUID
	if err := s.db.WithContext(ctx).Model(&models.Certificate{}).
		Where("issuer = ? AND status = ?", acmeclient.Issuer, CertificateStatusPending).Pluck("id", &ids).Error; err != nil {
		s.logger.Error("load pending ACME certificates", "error", err)
		return
	}
	for _, id := range ids {
		_ = s.startFlight(id)
	}
}

func (s *CertificateService) renewDue(ctx context.Context) {
	if !s.options.AutoRenew {
		return
	}
	now := time.Now().UTC()
	threshold := now.Add(s.options.RenewBefore)
	var items []models.Certificate
	if err := s.db.WithContext(ctx).
		Where("issuer = ? AND auto_renew = ? AND status = ?", acmeclient.Issuer, true, CertificateStatusActive).
		Where("expires_at IS NOT NULL AND expires_at < ?", threshold).
		Find(&items).Error; err != nil {
		s.logger.Error("scan certificates for renewal", "error", err)
		return
	}
	for _, item := range items {
		if !ShouldRenew(item, now, s.options.RenewBefore, s.options.AutoRenew) {
			continue
		}
		if err := s.startFlight(item.ID); err != nil && !errors.Is(err, ErrOrderInFlight) {
			s.logger.Warn("queue automatic certificate renewal", "certificate_id", item.ID, "error", err)
		}
	}
}

func (s *CertificateService) refreshExpiryMetrics(ctx context.Context) {
	if s.metrics == nil {
		return
	}
	var items []models.Certificate
	if err := s.db.WithContext(ctx).Where("expires_at IS NOT NULL").Order("domain, expires_at DESC").Find(&items).Error; err != nil {
		s.logger.Error("scan certificate expiry metrics", "error", err)
		return
	}
	latest := make(map[string]models.Certificate)
	for _, item := range items {
		if _, ok := latest[item.Domain]; !ok {
			latest[item.Domain] = item
		}
	}
	now := time.Now().UTC()
	for domain, item := range latest {
		if item.ExpiresAt == nil {
			continue
		}
		s.metrics.SetCertificateExpiry(domain, item.ExpiresAt.Sub(now).Hours()/24)
	}
}

// ShouldRenew is the central renewal decision used by the scheduler and tests.
func ShouldRenew(item models.Certificate, now time.Time, renewBefore time.Duration, autoRenewEnabled bool) bool {
	if !autoRenewEnabled || item.Issuer != acmeclient.Issuer || !item.AutoRenew || item.Status != CertificateStatusActive || item.ExpiresAt == nil {
		return false
	}
	if item.NextAttemptAt != nil && item.NextAttemptAt.After(now) {
		return false
	}
	return item.ExpiresAt.Sub(now) < renewBefore
}

func certificateBackoff(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	if failures > 6 {
		failures = 6
	}
	return 30 * time.Minute * time.Duration(1<<(failures-1))
}

func truncateError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 2000 {
		return value[:2000]
	}
	return value
}

func certificateDomain(dnsNames []string, commonName string) (string, error) {
	names := append(append([]string(nil), dnsNames...), commonName)
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if domain, err := validation.Domain(name); err == nil {
			return domain, nil
		}
	}
	return "", errors.New("certificate must contain a valid non-wildcard DNS name")
}
