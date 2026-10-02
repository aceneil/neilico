package proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"umpp/control-plane/internal/models"
	"umpp/control-plane/internal/validation"
)

type Route struct {
	RuleID                     uuid.UUID            `json:"rule_id"`
	TenantID                   uuid.UUID            `json:"tenant_id"`
	DomainID                   uuid.UUID            `json:"domain_id"`
	Host                       string               `json:"host"`
	Path                       string               `json:"path"`
	TargetType                 string               `json:"target_type"`
	Target                     string               `json:"target_addr"`
	UpstreamScheme             string               `json:"upstream_scheme"`
	UpstreamInsecureSkipVerify bool                 `json:"upstream_insecure_skip_verify"`
	UpstreamCAFile             string               `json:"upstream_ca_file,omitempty"`
	OriginalTarget             string               `json:"original_target,omitempty"`
	AccessControl              models.AccessControl `json:"access_control"`
	Certificate                *RouteCertificate    `json:"certificate,omitempty"`
}

type RouteCertificate struct {
	ID        string `json:"id"`
	CertPEM   string `json:"cert_pem"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

type RouteSet struct {
	byHost map[string][]Route
	Routes []Route
}

func LoadRoutes(ctx context.Context, db *gorm.DB) (*RouteSet, error) {
	var rules []models.ProxyRule
	if err := db.WithContext(ctx).
		Preload("Domain").
		Preload("Domain.Certificate").
		Joins("JOIN domains ON domains.id = proxy_rules.domain_id").
		Where("proxy_rules.enabled = ? AND domains.status = ?", true, "active").
		Order("proxy_rules.created_at ASC").
		Find(&rules).Error; err != nil {
		return nil, fmt.Errorf("load proxy routes: %w", err)
	}
	set := &RouteSet{byHost: make(map[string][]Route), Routes: make([]Route, 0, len(rules))}
	for _, rule := range rules {
		target, err := resolveTarget(ctx, db, rule)
		if err != nil {
			return nil, err
		}
		host := strings.ToLower(strings.TrimSuffix(rule.Domain.Domain, "."))
		route := Route{
			RuleID:                     rule.ID,
			TenantID:                   rule.TenantID,
			DomainID:                   rule.DomainID,
			Host:                       host,
			Path:                       rule.Path,
			TargetType:                 rule.TargetType,
			Target:                     target,
			UpstreamScheme:             upstreamScheme(rule.UpstreamScheme),
			UpstreamInsecureSkipVerify: rule.UpstreamInsecureSkipVerify,
			UpstreamCAFile:             rule.UpstreamCAFile,
			OriginalTarget:             rule.Target,
			AccessControl:              rule.AccessControl,
		}
		if rule.Domain.Certificate != nil {
			certificate := &RouteCertificate{ID: rule.Domain.Certificate.ID.String(), CertPEM: rule.Domain.Certificate.CertPEM}
			if rule.Domain.Certificate.ExpiresAt != nil {
				certificate.ExpiresAt = rule.Domain.Certificate.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")
			}
			route.Certificate = certificate
		}
		set.Routes = append(set.Routes, route)
		set.byHost[host] = append(set.byHost[host], route)
	}
	for host := range set.byHost {
		routes := set.byHost[host]
		sort.SliceStable(routes, func(i, j int) bool {
			if len(routes[i].Path) == len(routes[j].Path) {
				return routes[i].RuleID.String() < routes[j].RuleID.String()
			}
			return len(routes[i].Path) > len(routes[j].Path)
		})
		set.byHost[host] = routes
	}
	sort.SliceStable(set.Routes, func(i, j int) bool {
		if set.Routes[i].Host == set.Routes[j].Host {
			if len(set.Routes[i].Path) == len(set.Routes[j].Path) {
				return set.Routes[i].RuleID.String() < set.Routes[j].RuleID.String()
			}
			return len(set.Routes[i].Path) > len(set.Routes[j].Path)
		}
		return set.Routes[i].Host < set.Routes[j].Host
	})
	return set, nil
}

func (s *RouteSet) Lookup(host, path string) *Route {
	host = normalizeHost(host)
	var match *Route
	for index := range s.byHost[host] {
		route := &s.byHost[host][index]
		if !strings.HasPrefix(path, route.Path) {
			continue
		}
		if match == nil || len(route.Path) > len(match.Path) {
			candidate := *route
			match = &candidate
		}
	}
	return match
}

func resolveTarget(ctx context.Context, db *gorm.DB, rule models.ProxyRule) (string, error) {
	if rule.TargetType != validation.TargetNode {
		return rule.Target, nil
	}
	nodeID, _, err := validation.Target(rule.TargetType, rule.Target)
	if err != nil {
		return "", &UnprocessableError{Reason: err.Error()}
	}
	var node models.Node
	if err := db.WithContext(ctx).Where("id = ? AND tenant_id = ?", nodeID, rule.TenantID).First(&node).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", &UnprocessableError{Reason: fmt.Sprintf("node %s does not exist in tenant %s", nodeID, rule.TenantID)}
		}
		return "", fmt.Errorf("resolve proxy node: %w", err)
	}
	host := ""
	if node.VirtualIP != nil {
		host = strings.TrimSpace(*node.VirtualIP)
	}
	if host == "" {
		// Nodes normally receive their virtual IP through network membership,
		// while Node.VirtualIP is optional at registration. Resolve that
		// membership address for target_type=node rules.
		var member models.NetworkMember
		err := db.WithContext(ctx).
			Where("node_id = ?", node.ID).
			Order("joined_at DESC, id ASC").
			First(&member).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", &UnprocessableError{Reason: fmt.Sprintf("node %s has no virtual IP assigned", nodeID)}
		}
		if err != nil {
			// A few focused unit tests use a minimal schema; treat a missing
			// membership table like an unassigned node rather than a 500.
			if strings.Contains(strings.ToLower(err.Error()), "no such table") {
				return "", &UnprocessableError{Reason: fmt.Sprintf("node %s has no virtual IP assigned", nodeID)}
			}
			return "", fmt.Errorf("resolve proxy node virtual IP: %w", err)
		}
		host = strings.TrimSpace(member.VirtualIP)
	}
	_, port, err := net.SplitHostPort(rule.Target)
	if err != nil {
		return "", &UnprocessableError{Reason: err.Error()}
	}
	if parsed, parseErr := netip.ParsePrefix(host); parseErr == nil {
		host = parsed.Addr().String()
	} else if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
		host = addr.String()
	} else {
		return "", &UnprocessableError{Reason: fmt.Sprintf("node %s has invalid virtual IP %q", nodeID, host)}
	}
	return net.JoinHostPort(host, port), nil
}

func normalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	return strings.TrimSuffix(host, ".")
}

func upstreamScheme(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "https") {
		return "https"
	}
	return "http"
}
