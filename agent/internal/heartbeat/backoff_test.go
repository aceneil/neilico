package heartbeat

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBackoffSequenceAndReset(t *testing.T) {
	backoff := NewBackoff(time.Second, 60*time.Second)
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second}
	for index, expected := range want {
		if got := backoff.Next(); got != expected {
			t.Fatalf("Next() #%d = %s, want %s", index, got, expected)
		}
	}
	backoff.Reset()
	if got := backoff.Next(); got != time.Second {
		t.Fatalf("Next() after reset = %s", got)
	}
}

func TestRetryUntilContinuesAfterFailures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	attempts := 0
	err := RetryUntil(ctx, func(context.Context) error {
		attempts++
		return errors.New("temporary")
	}, nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RetryUntil() error = %v", err)
	}
	if attempts < 2 {
		t.Fatalf("attempts = %d, want retry after failure", attempts)
	}
}
