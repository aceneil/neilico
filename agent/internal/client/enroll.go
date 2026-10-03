package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

type EnrollRequest struct {
	Token     string   `json:"token"`
	Name      string   `json:"name,omitempty"`
	Hostname  string   `json:"hostname,omitempty"`
	OS        string   `json:"os"`
	Arch      string   `json:"arch"`
	Version   string   `json:"version"`
	Tags      []string `json:"tags,omitempty"`
	PublicKey string   `json:"public_key,omitempty"`
}

type EnrollResponse struct {
	NodeID     string `json:"node_id"`
	AgentToken string `json:"agent_token"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key,omitempty"`
	VirtualIP  string `json:"virtual_ip,omitempty"`
	Server     string `json:"server"`
	NetworkID  string `json:"network_id,omitempty"`
	Replayed   bool   `json:"replayed,omitempty"`
}

func (c *Client) Enroll(ctx context.Context, input EnrollRequest) (EnrollResponse, error) {
	var output EnrollResponse
	if err := c.do(ctx, http.MethodPost, "/api/v1/nodes/enroll", input, &output); err != nil {
		return EnrollResponse{}, err
	}
	if output.NodeID == "" {
		return EnrollResponse{}, errors.New("enrollment response omitted node_id")
	}
	if output.Replayed {
		return output, nil
	}
	if output.AgentToken == "" || output.PrivateKey == "" {
		return EnrollResponse{}, fmt.Errorf("enrollment response omitted agent credentials")
	}
	return output, nil
}
