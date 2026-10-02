package pki

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"neilico/control-plane/internal/models"
	certcrypto "neilico/control-plane/internal/service/cert"
)

var ErrDisabled = errors.New("PKI is disabled; set pki.enabled=true to use internal certificates")

type Metrics interface {
	IncPKICertificatesIssued(kind string)
	SetPKICAExpiry(timestamp float64)
	SetPKICertificateExpiry(kind string, days float64)
}

type Options struct {
	Enabled         bool
	CommonName      string
	ServerHosts     []string
	ServerCertDays  int
	NodeCertDays    int
	RenewBeforeDays int
}

type Service struct {
	db      *gorm.DB
	crypto  *certcrypto.Crypto
	options Options
	metrics Metrics
}

type CAMetadata struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	CreatedAt time.Time `json:"created_at"`
}

type IssuedCertificate struct {
	CertPEM  string
	KeyPEM   string
	Metadata NodeCertificateMetadata
}

type NodeCertificateMetadata struct {
	ID           uuid.UUID `json:"id"`
	NodeID       uuid.UUID `json:"node_id"`
	SerialNumber string    `json:"serial_number"`
	Fingerprint  string    `json:"fingerprint"`
	NotBefore    time.Time `json:"not_before"`
	NotAfter     time.Time `json:"not_after"`
	IssuedAt     time.Time `json:"issued_at"`
	CAPEM        string    `json:"ca_cert_pem,omitempty"`
}

func New(db *gorm.DB, crypto *certcrypto.Crypto, options Options, observer Metrics) *Service {
	return &Service{db: db, crypto: crypto, options: options, metrics: observer}
}

func (s *Service) Enabled() bool { return s.options.Enabled }

func (s *Service) EnsureCA() (models.CA, error) {
	if !s.options.Enabled {
		return models.CA{}, ErrDisabled
	}
	var current models.CA
	err := s.db.Order("created_at DESC, id ASC").First(&current).Error
	if err == nil {
		s.observeCA(current)
		return current, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return models.CA{}, fmt.Errorf("load PKI CA: %w", err)
	}
	return s.createCA()
}

func (s *Service) RotateCA() (models.CA, error) {
	if !s.options.Enabled {
		return models.CA{}, ErrDisabled
	}
	return s.createCA()
}

func (s *Service) ActiveCA() (models.CA, error) {
	if !s.options.Enabled {
		return models.CA{}, ErrDisabled
	}
	var item models.CA
	err := s.db.Order("created_at DESC, id ASC").First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		item, err = s.EnsureCA()
	}
	if err != nil {
		return models.CA{}, err
	}
	s.observeCA(item)
	return item, nil
}

func (s *Service) CAPEM() (string, error) {
	item, err := s.ActiveCA()
	if err != nil {
		return "", err
	}
	return item.CertPEM, nil
}

func (s *Service) ClientCACertPool() (*x509.CertPool, error) {
	if !s.options.Enabled {
		return nil, ErrDisabled
	}
	if _, err := s.EnsureCA(); err != nil {
		return nil, err
	}
	var items []models.CA
	if err := s.db.Order("created_at ASC, id ASC").Find(&items).Error; err != nil {
		return nil, fmt.Errorf("load PKI trust anchors: %w", err)
	}
	pool := x509.NewCertPool()
	for _, item := range items {
		if !pool.AppendCertsFromPEM([]byte(item.CertPEM)) {
			return nil, fmt.Errorf("load PKI CA %s certificate PEM", item.ID)
		}
	}
	return pool, nil
}

func (s *Service) VerifyNodeCert(peer *x509.Certificate) error {
	if peer == nil {
		return errors.New("client certificate is required")
	}
	pool, err := s.ClientCACertPool()
	if err != nil {
		return err
	}
	if _, err := peer.Verify(x509.VerifyOptions{
		Roots:     pool,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return fmt.Errorf("verify node client certificate: %w", err)
	}
	return nil
}

func (s *Service) IssueServerCert(hosts []string) (IssuedCertificate, error) {
	ca, err := s.ActiveCA()
	if err != nil {
		return IssuedCertificate{}, err
	}
	caCert, caKey, err := s.caMaterial(ca)
	if err != nil {
		return IssuedCertificate{}, err
	}
	names := append([]string(nil), s.options.ServerHosts...)
	names = append(names, hosts...)
	names = append(names, "127.0.0.1", "localhost")
	dnsNames, ips := distinctSANs(names)
	if len(dnsNames) == 0 {
		dnsNames = []string{"localhost"}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return IssuedCertificate{}, fmt.Errorf("generate server key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return IssuedCertificate{}, err
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   firstServerName(dnsNames),
			Organization: []string{"NEILICO"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(time.Duration(s.options.ServerCertDays) * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, key.Public(), caKey)
	if err != nil {
		return IssuedCertificate{}, fmt.Errorf("sign server certificate: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return IssuedCertificate{}, fmt.Errorf("marshal server key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	s.observeIssued("server", template.NotAfter)
	return IssuedCertificate{CertPEM: string(certPEM), KeyPEM: string(keyPEM)}, nil
}

func (s *Service) IssueNodeCert(nodeID uuid.UUID, commonName string) (IssuedCertificate, error) {
	if nodeID == uuid.Nil {
		return IssuedCertificate{}, errors.New("node ID is required")
	}
	commonName = strings.TrimSpace(commonName)
	if commonName == "" {
		commonName = nodeID.String()
	}
	ca, err := s.ActiveCA()
	if err != nil {
		return IssuedCertificate{}, err
	}
	caCert, caKey, err := s.caMaterial(ca)
	if err != nil {
		return IssuedCertificate{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return IssuedCertificate{}, fmt.Errorf("generate node key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return IssuedCertificate{}, err
	}
	nodeURI, err := url.Parse("urn:uuid:" + nodeID.String())
	if err != nil {
		return IssuedCertificate{}, fmt.Errorf("build node certificate URI: %w", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"NEILICO"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.Add(time.Duration(s.options.NodeCertDays) * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		DNSNames:              distinctStrings([]string{commonName, nodeID.String()}),
		URIs:                  []*url.URL{nodeURI},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, key.Public(), caKey)
	if err != nil {
		return IssuedCertificate{}, fmt.Errorf("sign node certificate: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return IssuedCertificate{}, fmt.Errorf("marshal node key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	encryptedKey, err := s.crypto.Encrypt(string(keyPEM))
	if err != nil {
		return IssuedCertificate{}, fmt.Errorf("encrypt node private key: %w", err)
	}
	digest := sha256.Sum256(der)
	item := models.NodeCertificate{
		ID:              uuid.New(),
		NodeID:          nodeID,
		CAID:            ca.ID,
		SerialNumber:    serial.String(),
		Fingerprint:     "sha256:" + hex.EncodeToString(digest[:]),
		CertPEM:         string(certPEM),
		EncryptedKeyPEM: encryptedKey,
		NotBefore:       template.NotBefore,
		NotAfter:        template.NotAfter,
		CreatedAt:       now,
	}
	if err := s.db.Create(&item).Error; err != nil {
		return IssuedCertificate{}, fmt.Errorf("store node certificate: %w", err)
	}
	s.observeIssued("node", template.NotAfter)
	return IssuedCertificate{
		CertPEM: string(certPEM),
		KeyPEM:  string(keyPEM),
		Metadata: NodeCertificateMetadata{
			ID: item.ID, NodeID: nodeID, SerialNumber: item.SerialNumber,
			Fingerprint: item.Fingerprint, NotBefore: item.NotBefore,
			NotAfter: item.NotAfter, IssuedAt: item.CreatedAt,
		},
	}, nil
}

func (s *Service) NodeCertificateMetadata(ctx context.Context, nodeID uuid.UUID) (NodeCertificateMetadata, error) {
	var item models.NodeCertificate
	err := s.db.Where("node_id = ?", nodeID).Order("created_at DESC, id DESC").First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NodeCertificateMetadata{}, gorm.ErrRecordNotFound
	}
	if err != nil {
		return NodeCertificateMetadata{}, fmt.Errorf("load node certificate metadata: %w", err)
	}
	return NodeCertificateMetadata{
		ID: item.ID, NodeID: item.NodeID, SerialNumber: item.SerialNumber,
		Fingerprint: item.Fingerprint, NotBefore: item.NotBefore,
		NotAfter: item.NotAfter, IssuedAt: item.CreatedAt,
	}, nil
}

func (s *Service) ServerCertificate(hosts []string) (tls.Certificate, error) {
	issued, err := s.IssueServerCert(hosts)
	if err != nil {
		return tls.Certificate{}, err
	}
	pair, err := tls.X509KeyPair([]byte(issued.CertPEM), []byte(issued.KeyPEM))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("load issued server certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("parse issued server certificate: %w", err)
	}
	pair.Leaf = leaf
	return pair, nil
}

func (s *Service) createCA() (models.CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return models.CA{}, fmt.Errorf("generate CA key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return models.CA{}, err
	}
	now := time.Now().UTC()
	name := strings.TrimSpace(s.options.CommonName)
	if name == "" {
		name = "NEILICO Internal CA"
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   name,
			Organization: []string{"NEILICO"},
		},
		NotBefore:             now.Add(-5 * time.Minute),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return models.CA{}, fmt.Errorf("create CA certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return models.CA{}, fmt.Errorf("marshal CA key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	encryptedKey, err := s.crypto.Encrypt(string(keyPEM))
	if err != nil {
		return models.CA{}, fmt.Errorf("encrypt CA private key: %w", err)
	}
	item := models.CA{
		ID: uuid.New(), Name: name,
		CertPEM:         string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		EncryptedKeyPEM: encryptedKey,
		NotBefore:       template.NotBefore, NotAfter: template.NotAfter, CreatedAt: now,
	}
	if err := s.db.Create(&item).Error; err != nil {
		return models.CA{}, fmt.Errorf("store PKI CA: %w", err)
	}
	s.observeCA(item)
	s.observeIssued("ca", item.NotAfter)
	return item, nil
}

func (s *Service) caMaterial(item models.CA) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(item.CertPEM))
	if block == nil {
		return nil, nil, errors.New("decode PKI CA certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse PKI CA certificate: %w", err)
	}
	encryptedKey, err := s.crypto.Decrypt(item.EncryptedKeyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt PKI CA private key: %w", err)
	}
	keyBlock, _ := pem.Decode([]byte(encryptedKey))
	if keyBlock == nil {
		return nil, nil, errors.New("decode PKI CA private key PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse PKI CA private key: %w", err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok {
		return nil, nil, errors.New("PKI CA private key is not ECDSA")
	}
	return cert, key, nil
}

func (s *Service) observeCA(item models.CA) {
	if s.metrics != nil {
		s.metrics.SetPKICAExpiry(float64(item.NotAfter.Unix()))
	}
}

func (s *Service) observeIssued(kind string, notAfter time.Time) {
	if s.metrics == nil {
		return
	}
	s.metrics.IncPKICertificatesIssued(kind)
	s.metrics.SetPKICertificateExpiry(kind, time.Until(notAfter).Hours()/24)
}

func randomSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, fmt.Errorf("generate certificate serial: %w", err)
	}
	return serial, nil
}

func distinctSANs(values []string) ([]string, []net.IP) {
	var dns []string
	var ips []net.IP
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		host := value
		if parsed, _, err := net.SplitHostPort(value); err == nil {
			host = parsed
		}
		if ip := net.ParseIP(host); ip != nil {
			if !containsIP(ips, ip) {
				ips = append(ips, ip)
			}
			continue
		}
		dns = append(dns, strings.TrimSuffix(strings.ToLower(host), "."))
	}
	return distinctStrings(dns), ips
}

func distinctStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func containsIP(values []net.IP, candidate net.IP) bool {
	for _, value := range values {
		if value.Equal(candidate) {
			return true
		}
	}
	return false
}

func firstServerName(values []string) string {
	for _, value := range values {
		if value != "localhost" {
			return value
		}
	}
	return "localhost"
}
