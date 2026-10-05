package validation

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// StreamProtocol 归一化并校验端口转发协议。
func StreamProtocol(value string) (string, error) {
	protocol := strings.ToLower(strings.TrimSpace(value))
	if protocol == "" {
		protocol = "tcp"
	}
	if protocol != "tcp" && protocol != "udp" {
		return "", errors.New("protocol must be tcp or udp")
	}
	return protocol, nil
}

// StreamName 校验端口转发规则名。
func StreamName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if name == "" {
		return "", errors.New("name is required")
	}
	if len([]rune(name)) > 128 {
		return "", errors.New("name must be at most 128 characters")
	}
	return name, nil
}

// StreamListenPort 校验监听端口是否落在本部署「已对外发布」的端口段内。
//
// 端口段必须与容器发布的端口一致：容器内监听了、但没发布出去的端口，从外部是连不上的
// （规则会显示运行中却不可达），所以这里直接拦住。
func StreamListenPort(port, min, max int) (int, error) {
	if port < 1 || port > 65535 {
		return 0, errors.New("listen_port must be between 1 and 65535")
	}
	if port < 1024 {
		return 0, errors.New("listen_port must be 1024 or above (privileged ports are reserved)")
	}
	if min > 0 && port < min {
		return 0, fmt.Errorf("listen_port must be within the published range %d-%d", min, max)
	}
	if max > 0 && port > max {
		return 0, fmt.Errorf("listen_port must be within the published range %d-%d", min, max)
	}
	return port, nil
}

// StreamIPWhitelist 归一化并校验来源白名单（单个 IP 或 CIDR）。
// 空白名单=不限制；TCP/UDP 层面无法做 Basic/JWT（那是 HTTP 才有的语义）。
func StreamIPWhitelist(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(value); err != nil {
			if addr := net.ParseIP(value); addr == nil {
				return nil, fmt.Errorf("%q is not an IP address or CIDR", value)
			}
			value = net.ParseIP(value).String()
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

// StreamTarget 复用代理规则的目标语法：node | virtual_ip | internal_ip，且必须带端口。
func StreamTarget(targetType, target string) error {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return errors.New("target is required")
	}
	if _, _, err := Target(targetType, trimmed); err != nil {
		return err
	}
	return nil
}
