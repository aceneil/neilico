package mesh

import (
	"errors"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type parsedPeer struct {
	publicKey  string
	endpoint   string
	allowedIPs []string
	keepalive  int
}

type parsedConfig struct {
	privateKey string
	addresses  []string
	listenPort int
	mtu        int
	peers      []parsedPeer
}

func parseWireGuardConfig(value string) (parsedConfig, error) {
	var parsed parsedConfig
	section := ""
	for _, raw := range strings.Split(value, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			if section != "interface" && section != "peer" {
				return parsedConfig{}, errors.New("unsupported WireGuard section " + section)
			}
			if section == "peer" {
				parsed.peers = append(parsed.peers, parsedPeer{})
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return parsedConfig{}, errors.New("invalid WireGuard configuration line")
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch {
		case section == "interface":
			switch key {
			case "privatekey":
				parsed.privateKey = value
			case "address":
				parsed.addresses = append(parsed.addresses, strings.Split(value, ",")...)
			case "listenport":
				port, err := strconv.Atoi(value)
				if err != nil {
					return parsedConfig{}, errors.New("invalid listen port")
				}
				parsed.listenPort = port
			case "mtu":
				mtu, err := strconv.Atoi(value)
				if err != nil {
					return parsedConfig{}, errors.New("invalid MTU")
				}
				parsed.mtu = mtu
			}
		case section == "peer" && len(parsed.peers) > 0:
			peer := &parsed.peers[len(parsed.peers)-1]
			switch key {
			case "publickey":
				peer.publicKey = value
			case "endpoint":
				peer.endpoint = value
			case "allowedips":
				for _, item := range strings.Split(value, ",") {
					if item = strings.TrimSpace(item); item != "" {
						peer.allowedIPs = append(peer.allowedIPs, item)
					}
				}
			case "persistentkeepalive":
				seconds, err := strconv.Atoi(value)
				if err != nil {
					return parsedConfig{}, errors.New("invalid persistent keepalive")
				}
				peer.keepalive = seconds
			}
		}
	}
	if parsed.privateKey == "" {
		return parsedConfig{}, errors.New("WireGuard configuration omitted PrivateKey")
	}
	return parsed, nil
}

func wgConfig(parsed parsedConfig, replacePeers bool) (wgtypes.Config, error) {
	privateKey, err := wgtypes.ParseKey(parsed.privateKey)
	if err != nil {
		return wgtypes.Config{}, err
	}
	result := wgtypes.Config{
		PrivateKey:   &privateKey,
		ReplacePeers: replacePeers,
	}
	if parsed.listenPort > 0 {
		port := parsed.listenPort
		result.ListenPort = &port
	}
	for _, raw := range parsed.peers {
		publicKey, err := wgtypes.ParseKey(raw.publicKey)
		if err != nil {
			return wgtypes.Config{}, err
		}
		peer := wgtypes.PeerConfig{
			PublicKey:         publicKey,
			ReplaceAllowedIPs: true,
		}
		if raw.endpoint != "" {
			address, err := net.ResolveUDPAddr("udp", raw.endpoint)
			if err != nil {
				return wgtypes.Config{}, err
			}
			peer.Endpoint = address
		}
		if raw.keepalive > 0 {
			keepalive := time.Duration(raw.keepalive) * time.Second
			peer.PersistentKeepaliveInterval = &keepalive
		}
		for _, rawCIDR := range raw.allowedIPs {
			_, cidr, err := net.ParseCIDR(rawCIDR)
			if err != nil {
				return wgtypes.Config{}, err
			}
			peer.AllowedIPs = append(peer.AllowedIPs, *cidr)
		}
		result.Peers = append(result.Peers, peer)
	}
	return result, nil
}
