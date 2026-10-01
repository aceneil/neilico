package heartbeat

import (
	"context"
	"log/slog"
	"time"
)

type Backoff struct {
	initial time.Duration
	maximum time.Duration
	current time.Duration
}

func NewBackoff(initial, maximum time.Duration) *Backoff {
	if initial <= 0 {
		initial = time.Second
	}
	if maximum < initial {
		maximum = initial
	}
	return &Backoff{initial: initial, maximum: maximum}
}

func (b *Backoff) Next() time.Duration {
	if b.current == 0 {
		b.current = b.initial
	} else if b.current < b.maximum {
		b.current *= 2
		if b.current > b.maximum {
			b.current = b.maximum
		}
	}
	return b.current
}

func (b *Backoff) Reset() { b.current = 0 }

type Observer interface {
	Heartbeat(result string)
}

type Sender func(context.Context) (time.Duration, error)

func Run(ctx context.Context, baseInterval time.Duration, sender Sender, observer Observer, logger *slog.Logger) {
	interval := baseInterval
	backoff := NewBackoff(time.Second, 60*time.Second)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		next, err := sender(ctx)
		if err != nil {
			if observer != nil {
				observer.Heartbeat("failure")
			}
			if logger != nil {
				logger.Warn("heartbeat failed", "error", err)
			}
			delay := backoff.Next()
			timer.Reset(delay)
			continue
		}
		if observer != nil {
			observer.Heartbeat("success")
		}
		backoff.Reset()
		if next > 0 {
			interval = next
		}
		timer.Reset(interval)
	}
}

func RetryUntil(ctx context.Context, operation func(context.Context) error, onRetry func(error), logger *slog.Logger) error {
	backoff := NewBackoff(time.Second, 60*time.Second)
	for {
		err := operation(ctx)
		if err == nil {
			return nil
		}
		if onRetry != nil {
			onRetry(err)
		}
		if logger != nil {
			logger.Warn("operation failed; retrying", "error", err)
		}
		timer := time.NewTimer(backoff.Next())
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
