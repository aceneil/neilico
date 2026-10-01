package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

type Notifier interface {
	Name() string
	Notify(ctx context.Context, notification Notification) error
}

type LogNotifier struct {
	logger *slog.Logger
}

func NewLogNotifier(logger *slog.Logger) *LogNotifier {
	if logger == nil {
		logger = slog.Default()
	}
	return &LogNotifier{logger: logger}
}

func (n *LogNotifier) Name() string { return "log" }

func (n *LogNotifier) Notify(_ context.Context, notification Notification) error {
	n.logger.Info("alert notification",
		"event", notification.Event,
		"alert_id", notification.Alert.ID,
		"rule", notification.Alert.Rule,
		"severity", notification.Alert.Severity,
		"target_type", notification.Alert.TargetType,
		"target_id", notification.Alert.TargetID,
		"value", notification.Alert.Value,
		"threshold", notification.Alert.Threshold,
	)
	return nil
}

type WebhookNotifier struct {
	url     string
	client  *http.Client
	retries int
	logger  *slog.Logger
}

func NewWebhookNotifier(url string, timeout time.Duration, retries int, logger *slog.Logger) (*WebhookNotifier, error) {
	if url == "" {
		return nil, errors.New("webhook URL is required")
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if retries < 0 {
		retries = 0
	}
	if retries > 3 {
		retries = 3
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &WebhookNotifier{
		url: url, client: &http.Client{Timeout: timeout},
		retries: retries, logger: logger,
	}, nil
}

func (n *WebhookNotifier) Name() string { return "webhook" }

func (n *WebhookNotifier) Notify(ctx context.Context, notification Notification) error {
	payload, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("marshal webhook notification: %w", err)
	}
	var lastErr error
	for attempt := 0; attempt <= n.retries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(attempt) * 100 * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, n.url, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("create webhook request: %w", err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", "umpp-alert-notifier/1")
		response, err := n.client.Do(request)
		if err != nil {
			lastErr = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("webhook returned HTTP %d", response.StatusCode)
	}
	return fmt.Errorf("webhook failed after %d attempt(s): %w", n.retries+1, lastErr)
}

type MultiNotifier struct {
	notifiers []Notifier
}

func NewMultiNotifier(notifiers ...Notifier) *MultiNotifier {
	active := make([]Notifier, 0, len(notifiers))
	for _, notifier := range notifiers {
		if notifier != nil {
			active = append(active, notifier)
		}
	}
	return &MultiNotifier{notifiers: active}
}

func (n *MultiNotifier) Name() string { return "multi" }

func (n *MultiNotifier) Notify(ctx context.Context, notification Notification) error {
	var combined error
	for _, notifier := range n.notifiers {
		if err := notifier.Notify(ctx, notification); err != nil {
			combined = errors.Join(combined, fmt.Errorf("%s: %w", notifier.Name(), err))
		}
	}
	return combined
}
