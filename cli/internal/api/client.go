package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
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

type TLSOptions struct {
	CAFile         string
	ClientCertFile string
	ClientKeyFile  string
}

func NewWithTLS(server, token string, options TLSOptions) (*Client, error) {
	if options.CAFile == "" && options.ClientCertFile == "" && options.ClientKeyFile == "" {
		return New(server, token), nil
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if options.CAFile != "" {
		data, err := os.ReadFile(options.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read TLS CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, errors.New("TLS CA file contains no certificates")
		}
		tlsConfig.RootCAs = pool
	}
	if options.ClientCertFile != "" || options.ClientKeyFile != "" {
		pair, err := tls.LoadX509KeyPair(options.ClientCertFile, options.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load TLS client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{pair}
	}
	transport.TLSClientConfig = tlsConfig
	return &Client{BaseURL: strings.TrimRight(server, "/"), Token: token, HTTP: &http.Client{Timeout: 15 * time.Second, Transport: transport}}, nil
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
			Error   json.RawMessage `json:"error"`
			Message string          `json:"message"`
		}
		_ = json.Unmarshal(payload, &envelope)
		code, message := parseErrorEnvelope(envelope.Error, envelope.Message)
		if message == "" {
			message = strings.TrimSpace(string(payload))
		}
		if code == "" {
			code = http.StatusText(response.StatusCode)
		}
		message = secretPattern.ReplaceAllString(message, `${1}***`)
		message = embeddedSecretPattern.ReplaceAllStringFunc(message, func(value string) string {
			return value[:8] + "…"
		})
		return &Error{Status: response.StatusCode, Code: code, Message: message}
	}
	if output != nil && len(payload) > 0 {
		if err := json.Unmarshal(payload, output); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func parseErrorEnvelope(raw json.RawMessage, fallback string) (string, string) {
	if len(raw) == 0 {
		return "", fallback
	}
	var object struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &object); err == nil {
		return object.Code, object.Message
	}
	var code string
	if err := json.Unmarshal(raw, &code); err == nil {
		return code, fallback
	}
	return "", fallback
}

var secretPattern = regexp.MustCompile(`(?i)("?(?:private_key|agent_token|network_secret|refresh_token)"?\s*[:=]\s*)("[^"]*"|\S+)`)
var embeddedSecretPattern = regexp.MustCompile(`(umpp_[A-Za-z0-9_-]{20,})`)
