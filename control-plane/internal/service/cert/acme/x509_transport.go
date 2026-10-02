package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	xacme "golang.org/x/crypto/acme"

	parentcert "neilico/control-plane/internal/service/cert"
)

// X509Transport is the RFC 8555 implementation backed by x/crypto/acme.
type X509Transport struct {
	config Config

	mu         sync.Mutex
	accountKey crypto.Signer
	client     *xacme.Client
	registered bool
}

func NewX509Transport(config Config) *X509Transport {
	return &X509Transport{config: config}
}

func (t *X509Transport) Register(ctx context.Context, email string) error {
	client, err := t.ensureClient()
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.registered && client.KID != "" {
		return nil
	}
	account, err := client.Register(ctx, &xacme.Account{Contact: []string{"mailto:" + strings.TrimSpace(email)}}, xacme.AcceptTOS)
	if err != nil && !errors.Is(err, xacme.ErrAccountAlreadyExists) {
		return fmt.Errorf("register ACME account: %w", err)
	}
	if account != nil && account.URI != "" {
		client.KID = xacme.KeyID(account.URI)
	}
	t.registered = true
	return nil
}

func (t *X509Transport) Obtain(ctx context.Context, domain string, solver ChallengeSolver) ([]byte, []byte, time.Time, error) {
	if t.config.Challenge != ChallengeHTTP01 {
		return nil, nil, time.Time{}, fmt.Errorf("%w: %s", ErrUnsupportedChallenge, t.config.Challenge)
	}
	client, err := t.ensureClient()
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	if err := t.Register(ctx, t.config.Email); err != nil {
		return nil, nil, time.Time{}, err
	}
	order, err := client.AuthorizeOrder(ctx, xacme.DomainIDs(domain))
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("create ACME order: %w", err)
	}
	for _, authzURL := range order.AuthzURLs {
		var authz *xacme.Authorization
		getErr := retryACMEBadNonce(ctx, func() error {
			var requestErr error
			authz, requestErr = client.GetAuthorization(ctx, authzURL)
			return requestErr
		})
		if getErr != nil {
			return nil, nil, time.Time{}, fmt.Errorf("get ACME authorization: %w", getErr)
		}
		if authz.Status == xacme.StatusValid {
			continue
		}
		challenge := findChallenge(authz, ChallengeHTTP01)
		if challenge == nil {
			return nil, nil, time.Time{}, fmt.Errorf("%w: authorization %s offers no http-01 challenge", ErrUnsupportedChallenge, authz.Identifier.Value)
		}
		keyAuthorization, responseErr := client.HTTP01ChallengeResponse(challenge.Token)
		if responseErr != nil {
			return nil, nil, time.Time{}, fmt.Errorf("compute HTTP-01 key authorization: %w", responseErr)
		}
		if solveErr := solver.Solve(ctx, domain, challenge.Token, keyAuthorization); solveErr != nil {
			return nil, nil, time.Time{}, fmt.Errorf("provision HTTP-01 challenge: %w", solveErr)
		}
		defer solver.Cleanup(context.WithoutCancel(ctx), domain, challenge.Token)
		acceptErr := retryACMEBadNonce(ctx, func() error {
			_, requestErr := client.Accept(ctx, challenge)
			return requestErr
		})
		if acceptErr != nil {
			return nil, nil, time.Time{}, fmt.Errorf("accept HTTP-01 challenge: %w", acceptErr)
		}
		waitErr := retryACMEBadNonce(ctx, func() error {
			_, requestErr := client.WaitAuthorization(ctx, authzURL)
			return requestErr
		})
		if waitErr != nil {
			return nil, nil, time.Time{}, fmt.Errorf("validate HTTP-01 challenge: %w", waitErr)
		}
	}
	readyOrder, err := client.WaitOrder(ctx, order.URI)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("wait for ACME order: %w", err)
	}
	finalizeURL := readyOrder.FinalizeURL
	if finalizeURL == "" {
		finalizeURL = order.FinalizeURL
	}
	if finalizeURL == "" {
		refetched, getErr := client.GetOrder(ctx, order.URI)
		if getErr != nil {
			return nil, nil, time.Time{}, fmt.Errorf("refresh ACME order: %w", getErr)
		}
		finalizeURL = refetched.FinalizeURL
	}
	if finalizeURL == "" {
		return nil, nil, time.Time{}, errors.New("ACME order response omitted finalize URL")
	}
	leafKey, err := generateCertificateKey(t.config.KeyType)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domain},
		DNSNames: []string{domain},
	}, leafKey)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("create certificate request: %w", err)
	}
	chainDER, certURL, err := client.CreateOrderCert(ctx, finalizeURL, csrDER, true)
	if err != nil {
		// Pebble may finalize without a Location header. x/crypto/acme then
		// internally waits on an empty order URL. The original order URL is
		// authoritative, so recover the issued chain through that URL.
		refreshed, refreshErr := client.GetOrder(ctx, order.URI)
		if refreshErr != nil {
			return nil, nil, time.Time{}, fmt.Errorf("finalize ACME order: %w", err)
		}
		if refreshed.Status != xacme.StatusValid {
			refreshed, refreshErr = client.WaitOrder(ctx, order.URI)
			if refreshErr != nil {
				return nil, nil, time.Time{}, fmt.Errorf("finalize ACME order: %w", err)
			}
		}
		if refreshed.CertURL == "" {
			return nil, nil, time.Time{}, fmt.Errorf("finalize ACME order: %w", err)
		}
		certURL = refreshed.CertURL
		chainDER, err = client.FetchCert(ctx, certURL, true)
		if err != nil {
			return nil, nil, time.Time{}, fmt.Errorf("fetch finalized ACME certificate: %w", err)
		}
	}
	if len(chainDER) == 0 {
		return nil, nil, time.Time{}, errors.New("ACME certificate response was empty")
	}
	certPEM := make([]byte, 0, len(chainDER)*256)
	for _, der := range chainDER {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("marshal certificate private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	leaf, err := parentcert.ParseAndMatch(string(certPEM), string(keyPEM))
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("validate issued certificate: %w", err)
	}
	if err := leaf.VerifyHostname(domain); err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("issued certificate does not match %s: %w", domain, err)
	}
	return certPEM, keyPEM, leaf.NotAfter.UTC(), nil
}

func (t *X509Transport) Revoke(context.Context, []byte) error {
	return fmt.Errorf("%w: certificate revocation", ErrNotImplemented)
}

func (t *X509Transport) ensureClient() (*xacme.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.client != nil {
		return t.client, nil
	}
	accountKey, err := generateAccountKey()
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{Timeout: 45 * time.Second}
	if strings.TrimSpace(t.config.CACertFile) != "" {
		caPEM, readErr := os.ReadFile(t.config.CACertFile)
		if readErr != nil {
			return nil, fmt.Errorf("read ACME CA certificate: %w", readErr)
		}
		pool, poolErr := x509.SystemCertPool()
		if poolErr != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("ACME CA certificate file contains no certificates")
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
		httpClient.Transport = transport
	}
	t.accountKey = accountKey
	t.client = &xacme.Client{
		Key:          accountKey,
		HTTPClient:   httpClient,
		DirectoryURL: t.config.DirectoryURL,
		RetryBackoff: func(n int, _ *http.Request, _ *http.Response) time.Duration {
			if n > 5 {
				return -1
			}
			return 50 * time.Millisecond
		},
		UserAgent: "neilico-control-plane/1.0",
	}
	return t.client, nil
}

func generateAccountKey() (crypto.Signer, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ACME account key: %w", err)
	}
	return key, nil
}

func generateCertificateKey(keyType string) (crypto.Signer, error) {
	switch keyType {
	case KeyTypeEC256:
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate EC certificate key: %w", err)
		}
		return key, nil
	case KeyTypeRSA2048:
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, fmt.Errorf("generate RSA certificate key: %w", err)
		}
		return key, nil
	default:
		return nil, fmt.Errorf("unsupported ACME key type %q", keyType)
	}
}

func retryACMEBadNonce(ctx context.Context, call func() error) error {
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		err = call()
		var acmeErr *xacme.Error
		if err == nil || !errors.As(err, &acmeErr) || !strings.Contains(strings.ToLower(acmeErr.ProblemType), "badnonce") {
			return err
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func findChallenge(authz *xacme.Authorization, challengeType string) *xacme.Challenge {
	for _, challenge := range authz.Challenges {
		if challenge != nil && challenge.Type == challengeType {
			return challenge
		}
	}
	return nil
}
