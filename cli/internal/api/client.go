package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("control plane returned %d %s: %s", e.Status, e.Code, e.Message)
}

func New(server, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(server, "/"), Token: token, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) Do(ctx context.Context, method, path string, input, output any) error {
	if c.BaseURL == "" {
		return fmt.Errorf("server is required")
	}
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	if c.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return fmt.Errorf("call control plane: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var envelope struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(payload, &envelope)
		message := envelope.Message
		if message == "" {
			message = strings.TrimSpace(string(payload))
		}
		if envelope.Error == "" {
			envelope.Error = http.StatusText(response.StatusCode)
		}
		message = secretPattern.ReplaceAllString(message, `${1}***`)
		return &Error{Status: response.StatusCode, Code: envelope.Error, Message: message}
	}
	if output != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, output); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

var secretPattern = regexp.MustCompile(`(?i)("?(?:private_key|agent_token|network_secret)"?\s*[:=]\s*)("[^"]*"|\S+)`)
