package acme

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ChallengeHTTP01 = "http-01"
	ChallengeDNS01  = "dns-01"

	KeyTypeEC256   = "ec256"
	KeyTypeRSA2048 = "rsa2048"

	Issuer = "acme"
)

var (
	ErrDisabled             = errors.New("acme is disabled")
	ErrTOSNotAccepted       = errors.New("ACME terms of service have not been accepted")
	ErrNotImplemented       = errors.New("not implemented")
	ErrInvalidChallenge     = errors.New("invalid ACME challenge")
	ErrUnsupportedChallenge = errors.New("unsupported ACME challenge")
)

// ErrorCode is returned to the API with a stable machine-readable code.
type Error struct {
	Code string
	Err  error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

func coded(code string, err error) error {
	return &Error{Code: code, Err: err}
}

func ErrorCode(err error) string {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	switch {
	case errors.Is(err, ErrDisabled):
		return "acme_disabled"
	case errors.Is(err, ErrTOSNotAccepted):
		return "acme_tos_not_accepted"
	case errors.Is(err, ErrNotImplemented):
		return "not_implemented"
	default:
		return "acme_error"
	}
}

type Config struct {
	Enabled         bool
	DirectoryURL    string
	Email           string
	Challenge       string
	HTTPPort        int
	RenewBeforeDays int
	CheckInterval   time.Duration
	KeyType         string
	AgreeTOS        bool
	CACertFile      string
	AutoRenew       bool
}

// Transport is the injectable ACME boundary. The production implementation is
// backed by golang.org/x/crypto/acme; tests inject a counting fake.
type Transport interface {
	Register(ctx context.Context, email string) error
	Obtain(ctx context.Context, domain string, challenge ChallengeSolver) (certPEM, keyPEM []byte, notAfter time.Time, err error)
	Revoke(ctx context.Context, certPEM []byte) error
}

// Client exposes the controlled lifecycle API while enforcing configuration
// guards before any transport call can reach the network.
type Client interface {
	Validate(challengeType string) error
	Register(ctx context.Context, email string) error
	Obtain(ctx context.Context, domain string, challenge ChallengeSolver) ([]byte, []byte, time.Time, error)
	Revoke(ctx context.Context, certPEM []byte) error
}

// ExternalAccountBinding is reserved for a later EAB implementation.
type ExternalAccountBinding interface {
	ExternalAccountBinding(ctx context.Context) (kid string, hmacKey []byte, err error)
}

type client struct {
	config    Config
	transport Transport
}

func New(config Config, transport Transport) Client {
	return &client{config: config, transport: transport}
}

func (c *client) Validate(challengeType string) error {
	return c.preflight(challengeType)
}

func (c *client) preflight(challengeType string) error {
	if !c.config.Enabled {
		return coded("acme_disabled", ErrDisabled)
	}
	if !c.config.AgreeTOS {
		return coded("acme_tos_not_accepted", ErrTOSNotAccepted)
	}
	if c.transport == nil {
		return coded("acme_transport_missing", errors.New("ACME transport is not configured"))
	}
	if challengeType != "" && challengeType != ChallengeHTTP01 {
		if challengeType == ChallengeDNS01 {
			return coded("acme_challenge_not_implemented", fmt.Errorf("%w: dns-01", ErrNotImplemented))
		}
		return coded("acme_challenge_unsupported", fmt.Errorf("%w: %s", ErrUnsupportedChallenge, challengeType))
	}
	return nil
}

func (c *client) Register(ctx context.Context, email string) error {
	if err := c.preflight(""); err != nil {
		return err
	}
	email = strings.TrimSpace(email)
	if email == "" {
		email = strings.TrimSpace(c.config.Email)
	}
	if email == "" {
		return coded("acme_email_required", errors.New("ACME registration email is required"))
	}
	if err := c.transport.Register(ctx, email); err != nil {
		return coded("acme_register_failed", err)
	}
	return nil
}

func (c *client) Obtain(ctx context.Context, domain string, challenge ChallengeSolver) ([]byte, []byte, time.Time, error) {
	if err := c.preflight(c.config.Challenge); err != nil {
		return nil, nil, time.Time{}, err
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return nil, nil, time.Time{}, coded("invalid_domain", errors.New("domain is required"))
	}
	if challenge == nil {
		return nil, nil, time.Time{}, coded("acme_challenge_missing", errors.New("challenge solver is required"))
	}
	certPEM, keyPEM, notAfter, err := c.transport.Obtain(ctx, domain, challenge)
	if err != nil {
		return nil, nil, time.Time{}, coded("acme_order_failed", err)
	}
	return certPEM, keyPEM, notAfter, nil
}

func (c *client) Revoke(ctx context.Context, certPEM []byte) error {
	if err := c.preflight(""); err != nil {
		return err
	}
	if err := c.transport.Revoke(ctx, certPEM); err != nil {
		if errors.Is(err, ErrNotImplemented) {
			return coded("acme_revoke_not_implemented", err)
		}
		return coded("acme_revoke_failed", err)
	}
	return nil
}
