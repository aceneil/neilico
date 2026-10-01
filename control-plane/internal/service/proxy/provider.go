package proxy

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrUnprocessable = errors.New("unprocessable proxy configuration")

type UnprocessableError struct {
	Reason string
}

func (e *UnprocessableError) Error() string {
	return "proxy configuration is unprocessable: " + e.Reason
}

func (e *UnprocessableError) Is(target error) bool { return target == ErrUnprocessable }

type Provider interface {
	Render(ctx context.Context, tenantID uuid.UUID) ([]byte, error)
	Kind() string
	Reload(ctx context.Context) error
}

type State struct {
	Kind      string    `json:"kind"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
	LastError string    `json:"last_error,omitempty"`
}

type StateProvider interface {
	State() State
}

type Observer interface {
	SetProxyProviderUp(kind string, up bool)
	ObserveProxyRequest(domain, status string)
}
