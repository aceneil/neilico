package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"umpp/agent/internal/state"
)

const agentVersion = "dev"

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("control plane returned %d %s: %s", e.StatusCode, e.Code, e.Message)
}

func New(server, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(server, "/"),
		token:   token,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Client) SetHTTPClient(httpClient *http.Client) {
	if httpClient != nil {
		c.http = httpClient
	}
}

func (c *Client) SetToken(token string) { c.token = token }

func (c *Client) Register(ctx context.Context, input RegisterRequest) (RegisterResponse, error) {
	var output RegisterResponse
	if err := c.do(ctx, http.MethodPost, "/api/v1/nodes/register", input, &output); err != nil {
		return RegisterResponse{}, err
	}
	if output.NodeID == "" || output.AgentToken == "" || output.PrivateKey == "" || output.PublicKey == "" {
		return RegisterResponse{}, errors.New("registration response omitted node identity or WireGuard keys")
	}
	return output, nil
}

func (c *Client) Heartbeat(ctx context.Context, nodeID string, version string) (HeartbeatResponse, error) {
	var output HeartbeatResponse
	path := "/api/v1/nodes/" + url.PathEscape(nodeID) + "/heartbeat"
	if err := c.do(ctx, http.MethodPost, path, HeartbeatRequest{Version: version}, &output); err != nil {
		return HeartbeatResponse{}, err
	}
	return output, nil
}

func (c *Client) ReportEndpoint(ctx context.Context, nodeID, endpoint string) error {
	path := "/api/v1/nodes/" + url.PathEscape(nodeID) + "/network-report"
	return c.do(ctx, http.MethodPost, path, NetworkReportRequest{PublicEndpoint: endpoint}, nil)
}

func (c *Client) Config(ctx context.Context, nodeID string, version int) (ConfigResult, error) {
	values := url.Values{}
	values.Set("node_id", nodeID)
	values.Set("version", fmt.Sprintf("%d", version))
	status, body, err := c.request(ctx, http.MethodGet, "/api/v1/agent/config?"+values.Encode(), nil)
	if err != nil {
		return ConfigResult{}, err
	}
	if status == http.StatusNotModified {
		var response struct {
			NotModified bool `json:"not_modified"`
			Version     int  `json:"version"`
		}
		if len(body) > 0 {
			_ = json.Unmarshal(body, &response)
		}
		if response.Version == 0 {
			response.Version = version
		}
		return ConfigResult{NotModified: true, Version: response.Version}, nil
	}
	if status < 200 || status >= 300 {
		return ConfigResult{}, decodeAPIError(status, body, c.token)
	}
	var delivery Delivery
	if err := json.Unmarshal(body, &delivery); err != nil {
		return ConfigResult{}, fmt.Errorf("decode agent config: %s", state.Sanitize(err.Error(), c.token))
	}
	return ConfigResult{Delivery: delivery}, nil
}

func (c *Client) ReportTraffic(ctx context.Context, nodeID string, entries []TrafficInput) error {
	if len(entries) == 0 {
		return nil
	}
	path := "/api/v1/nodes/" + url.PathEscape(nodeID) + "/traffic"
	return c.do(ctx, http.MethodPost, path, entries, nil)
}

func (c *Client) do(ctx context.Context, method, path string, input, output any) error {
	var body []byte
	if input != nil {
		var err error
		body, err = json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
	}
	status, response, err := c.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return decodeAPIError(status, response, c.token)
	}
	if output != nil && len(response) > 0 {
		if err := json.Unmarshal(response, output); err != nil {
			return fmt.Errorf("decode response: %s", state.Sanitize(err.Error(), c.token))
		}
	}
	return nil
}

func (c *Client) request(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("create request: %s", state.Sanitize(err.Error(), c.token))
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("call control plane: %s", state.Sanitize(err.Error(), c.token))
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("read response: %s", state.Sanitize(err.Error(), c.token))
	}
	return response.StatusCode, payload, nil
}

var (
	privateKeyPattern    = regexp.MustCompile(`(?i)("private_key"\s*:\s*")([^"]+)(")`)
	agentTokenPattern    = regexp.MustCompile(`(?i)("agent_token"\s*:\s*")([^"]+)(")`)
	iniSecretPattern     = regexp.MustCompile(`(?i)(PrivateKey\s*=\s*)(\S+)`)
	labeledSecretPattern = regexp.MustCompile(`(?i)((?:private(?:_key)?|agent_token|secret)\s*[:=]\s*)("[^"]*"|\S+)`)
)

func decodeAPIError(status int, body []byte, secret string) error {
	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &payload)
	message := payload.Message
	if message == "" {
		message = string(body)
	}
	message = state.Sanitize(message, secret)
	for _, pattern := range []*regexp.Regexp{privateKeyPattern, agentTokenPattern, iniSecretPattern} {
		for _, match := range pattern.FindAllStringSubmatch(message, -1) {
			for _, captured := range match[2:] {
				if captured != "" && captured != "***" {
					message = state.Sanitize(message, captured)
				}
			}
		}
	}
	message = privateKeyPattern.ReplaceAllString(message, `${1}***${3}`)
	message = agentTokenPattern.ReplaceAllString(message, `${1}***${3}`)
	message = iniSecretPattern.ReplaceAllString(message, `${1}***`)
	message = labeledSecretPattern.ReplaceAllString(message, `${1}***`)
	code := payload.Error
	if code == "" {
		code = http.StatusText(status)
	}
	return &APIError{StatusCode: status, Code: code, Message: message}
}

func SafeError(err error, secrets ...string) error {
	if err == nil {
		return nil
	}
	return errors.New(state.Sanitize(err.Error(), secrets...))
}

func AgentVersion() string { return agentVersion }
