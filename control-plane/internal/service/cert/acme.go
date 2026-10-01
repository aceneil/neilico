package cert

import (
	"context"
	"errors"
	"time"
)

const IssuerACMELetsEncrypt = "acme-letsencrypt"

// ErrNotImplemented marks the reserved ACME issuer API that is not part of M2a.
var ErrNotImplemented = errors.New("acme certificate issuance is not implemented")

type IssueResult struct {
	Domain    string
	CertPEM   string
	KeyPEM    string
	Issuer    string
	ExpiresAt time.Time
}

// Issuer is the M2b+ interface reserved for automatic certificate issuance.
type Issuer interface {
	Issue(ctx context.Context, domain string) (IssueResult, error)
}

type ACMEIssuer struct{}

func (ACMEIssuer) Issue(context.Context, string) (IssueResult, error) {
	return IssueResult{}, ErrNotImplemented
}
