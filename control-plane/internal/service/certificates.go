package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"umpp/control-plane/internal/models"
	"umpp/control-plane/internal/service/cert"
	"umpp/control-plane/internal/validation"
)

type CertificateService struct {
	db     *gorm.DB
	crypto *cert.Crypto
}

func NewCertificateService(db *gorm.DB, crypto *cert.Crypto) *CertificateService {
	return &CertificateService{db: db, crypto: crypto}
}

type CertificateInput struct {
	CertPEM string `json:"cert_pem"`
	KeyPEM  string `json:"key_pem"`
}

type CertificateList struct {
	Items    []models.Certificate `json:"items"`
	Total    int64                `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
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
	}
	if err := s.db.WithContext(ctx).Create(&item).Error; err != nil {
		return models.Certificate{}, fmt.Errorf("create certificate: %w", err)
	}
	return item, nil
}

func (s *CertificateService) Delete(ctx context.Context, id uuid.UUID, tenantID *uuid.UUID) error {
	item, err := s.Get(ctx, id, tenantID)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Domain{}).Where("cert_id = ?", item.ID).Updates(map[string]any{
			"cert_id": nil,
			"status":  DomainStatusPending,
		}).Error; err != nil {
			return fmt.Errorf("detach deleted certificate: %w", err)
		}
		if err := tx.Delete(&models.Certificate{}, "id = ?", item.ID).Error; err != nil {
			return fmt.Errorf("delete certificate: %w", err)
		}
		return nil
	})
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
