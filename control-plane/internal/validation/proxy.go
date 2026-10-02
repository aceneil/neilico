package validation

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"neilico/control-plane/internal/models"
)

const (
	TargetInternalIP = "internal_ip"
	TargetVirtualIP  = "virtual_ip"
	TargetNode       = "node"
)

func ProxyPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "/"
	}
	if !strings.HasPrefix(value, "/") {
		return "", errors.New("path must begin with /")
	}
	if strings.ContainsAny(value, "?#") {
		return "", errors.New("path must not contain a query string or fragment")
	}
	for _, char := range value {
		if char <= 0x20 || char == 0x7f {
			return "", errors.New("path must not contain spaces or control characters")
		}
	}
	return value, nil
}

func Target(targetType, target string) (uuid.UUID, string, error) {
	target = strings.TrimSpace(target)
	switch targetType {
	case TargetInternalIP, TargetVirtualIP:
		host, port, err := net.SplitHostPort(target)
		if err != nil {
			return uuid.Nil, "", fmt.Errorf("target must use host:port syntax: %w", err)
		}
		ip, err := netip.ParseAddr(host)
		if err != nil {
			return uuid.Nil, "", errors.New("target host must be an IP address")
		}
		if err := validatePort(port); err != nil {
			return uuid.Nil, "", err
		}
		if targetType == TargetVirtualIP && !ip.Is4() {
			return uuid.Nil, "", errors.New("virtual_ip target must be an IPv4 address")
		}
		return uuid.Nil, target, nil
	case TargetNode:
		host, port, err := net.SplitHostPort(target)
		if err != nil {
			return uuid.Nil, "", errors.New("node target must use <node_id>:<port> syntax")
		}
		nodeID, err := uuid.Parse(host)
		if err != nil {
			return uuid.Nil, "", errors.New("node target host must be a node UUID")
		}
		if err := validatePort(port); err != nil {
			return uuid.Nil, "", err
		}
		return nodeID, target, nil
	default:
		return uuid.Nil, "", errors.New("target_type must be internal_ip, virtual_ip, or node")
	}
}

func validatePort(value string) error {
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return errors.New("target port must be between 1 and 65535")
	}
	return nil
}

func AccessControl(value models.AccessControl) (models.AccessControl, error) {
	normalized := models.AccessControl{
		IPWhitelist: append([]string(nil), value.IPWhitelist...),
		BasicAuth:   value.BasicAuth,
		RequireJWT:  value.RequireJWT,
	}
	if normalized.IPWhitelist == nil {
		normalized.IPWhitelist = []string{}
	}
	for _, entry := range normalized.IPWhitelist {
		if strings.Contains(entry, "/") {
			if _, err := netip.ParsePrefix(entry); err != nil {
				return models.AccessControl{}, fmt.Errorf("invalid CIDR whitelist entry %q", entry)
			}
		} else if _, err := netip.ParseAddr(entry); err != nil {
			return models.AccessControl{}, fmt.Errorf("invalid IP whitelist entry %q", entry)
		}
	}
	basic := normalized.BasicAuth
	if basic.Enabled {
		basic.Username = strings.TrimSpace(basic.Username)
		if basic.Username == "" || basic.PasswordHash == "" {
			return models.AccessControl{}, errors.New("basic_auth username and password_hash are required when enabled")
		}
		if _, err := bcrypt.Cost([]byte(basic.PasswordHash)); err != nil {
			return models.AccessControl{}, errors.New("basic_auth password_hash must be a bcrypt hash")
		}
		normalized.BasicAuth = basic
	}
	return normalized, nil
}
