package mesh

// STUN（RFC 5389）Binding 探测：拿到「STUN 服务器观察到的本机公网映射地址」。
//
// 为什么自己手写而不引依赖：NEILICO 的 agent 要求零第三方依赖，而 STUN Binding
// Request 只是一条 20 字节头 + 一个属性，实现成本极低（见 RFC 5389 §6）。
//
// 为什么需要它：现有 public endpoint 靠 https://api.ipify.org 拿的是**出口 IP**，
// 而出口 IP ≠ WireGuard 端口的公网映射地址。NAT 可能给同一条物理连接分配与
// 监听端口不同的外部端口，于是对端按「公网IP:51820」拨过来时包根本到不了本机，
// mesh 永远建不起来。STUN 拿到的是「这个 UDP 端口在公网上长什么样」，正是打洞
// 需要的地址。
//
// 探测失败**不能**阻塞 agent 启动：所有服务器都失败时只记 warn，agent 的行为与
// 现在（只有 ipify 探测的 public endpoint）完全一致。

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	// STUNServersEnv 允许运维覆盖 STUN 服务器列表（逗号分隔）。
	STUNServersEnv = "NEILICO_AGENT_STUN_SERVERS"
	// DefaultSTUNServer 是默认使用的公共 STUN 服务器。
	DefaultSTUNServer = "stun.l.google.com:19302"
)

const (
	stunBindingRequest uint16 = 0x0001
	stunBindingSuccess uint16 = 0x0101
	stunBindingError   uint16 = 0x0111
	stunMagicCookie    uint32 = 0x2112A442

	stunHeaderSize        = 20
	stunTransactionIDSize = 12

	stunAttrXORMappedAddress uint16 = 0x0020
	stunAttrMappedAddress    uint16 = 0x0001

	stunDefaultPort = 19302
)

const (
	// stunTimeout 是单次 STUN 事务的等待上限。
	stunTimeout = 2 * time.Second
	// stunAttempts 是总尝试次数：首次 + 2 次重试。
	stunAttempts = 3
)

// Servers 返回配置好的 STUN 服务器列表：环境变量优先，逗号分隔。
//
// 环境变量为空时回落到默认公共服务器；调用方（agent）必须容忍这里返回的探测
// 失败——探测只是「锦上添花」的候选来源，失败不影响任何既有行为。
func Servers() []string {
	raw := strings.TrimSpace(os.Getenv(STUNServersEnv))
	if raw == "" {
		raw = DefaultSTUNServer
	}
	servers := make([]string, 0, 4)
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		servers = append(servers, item)
	}
	if len(servers) == 0 {
		servers = append(servers, DefaultSTUNServer)
	}
	return servers
}

// Discover 返回 STUN 服务器观察到的公网地址（ip:port）。
//
// localPort 是本机 WireGuard 的监听端口，返回的端口一律以它为准——对端要拨的是
// WireGuard 的监听端口，不是 STUN 探测用的临时端口（临时端口上的 NAT 映射对
// WireGuard 没有意义）。
//
// 探测本身**不绑定** localPort：WireGuard 已经在监听这个端口，抢它会让 WireGuard
// 建不起来；而且真要拿到「NAT 把 51820 映射成哪个外部端口」的信息，也得复用
// WireGuard 自己的套接字才能测（那是共享套接字的事，本期不做）。
//
// localPort <= 0 时原样返回服务器观察到的端口（仅用于测试/诊断）。
func Discover(ctx context.Context, server string, localPort int) (string, error) {
	return discover(ctx, server, localPort, stunTimeout, stunAttempts)
}

// DiscoverAny 依次尝试多个 STUN 服务器，第一个成功即返回；全部失败返回错误。
func DiscoverAny(ctx context.Context, servers []string, localPort int) (string, error) {
	if len(servers) == 0 {
		servers = Servers()
	}
	var lastErr error
	for _, server := range servers {
		address, err := Discover(ctx, server, localPort)
		if err == nil {
			return address, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的 STUN 服务器")
	}
	return "", fmt.Errorf("所有 STUN 服务器均探测失败: %w", lastErr)
}

func discover(ctx context.Context, server string, localPort int, timeout time.Duration, attempts int) (string, error) {
	server = strings.TrimSpace(server)
	if server == "" {
		return "", errors.New("stun: 服务器地址为空")
	}
	remote, err := net.ResolveUDPAddr("udp", stunServerAddr(server))
	if err != nil {
		return "", fmt.Errorf("stun: 解析服务器 %s 失败: %w", server, err)
	}
	conn, err := listenSTUN()
	if err != nil {
		return "", fmt.Errorf("stun: 打开本地 UDP 套接字失败: %w", err)
	}
	defer conn.Close()
	address, port, err := bindingTransaction(ctx, conn, remote, timeout, attempts)
	if err != nil {
		return "", fmt.Errorf("stun: %s 探测失败: %w", server, err)
	}
	if localPort > 0 {
		port = uint16(localPort)
	}
	return net.JoinHostPort(address.String(), strconv.Itoa(int(port))), nil
}

// stunServerAddr 补齐缺失的端口（RFC 5389 的默认端口是 19302）。
func stunServerAddr(server string) string {
	if _, _, err := net.SplitHostPort(server); err == nil {
		return server
	}
	return net.JoinHostPort(server, strconv.Itoa(stunDefaultPort))
}

// listenSTUN 打开本地 UDP 套接字（一律用临时端口，不碰 WireGuard 的监听端口）。
func listenSTUN() (*net.UDPConn, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv6unspecified, Port: 0})
	if err == nil {
		return conn, nil
	}
	return net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
}

// bindingTransaction 完成一次 STUN 绑定事务：发送 Binding Request，按 RFC 5389
// 的重传规则最多尝试 attempts 次，每次最多等待 timeout。
func bindingTransaction(ctx context.Context, conn *net.UDPConn, remote *net.UDPAddr, timeout time.Duration, attempts int) (netip.Addr, uint16, error) {
	if attempts < 1 {
		attempts = 1
	}
	var transactionID [stunTransactionIDSize]byte
	if _, err := rand.Read(transactionID[:]); err != nil {
		return netip.Addr{}, 0, fmt.Errorf("stun: 生成事务 ID 失败: %w", err)
	}
	request := buildBindingRequest(transactionID)
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if _, err := conn.WriteToUDP(request, remote); err != nil {
			lastErr = err
			continue
		}
		deadline := time.Now().Add(timeout)
		if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
			deadline = ctxDeadline
		}
		_ = conn.SetReadDeadline(deadline)
		buffer := make([]byte, 1500)
		n, _, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if ctx.Err() != nil {
				return netip.Addr{}, 0, ctx.Err()
			}
			lastErr = err
			continue
		}
		address, port, err := parseBindingResponse(buffer[:n], transactionID)
		if err != nil {
			lastErr = err
			continue
		}
		return address, port, nil
	}
	if lastErr == nil {
		lastErr = errors.New("没有收到响应")
	}
	return netip.Addr{}, 0, lastErr
}

// buildBindingRequest 构造 20 字节的 STUN Binding Request 头：
//
//	 0                   1                   2                   3
//	 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|0 0|     STUN Message Type     |         Message Length        |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|                         Magic Cookie                          |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|                                                               |
//	|                     Transaction ID (96 bits)                  |
//	|                                                               |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
func buildBindingRequest(transactionID [stunTransactionIDSize]byte) []byte {
	message := make([]byte, stunHeaderSize)
	binary.BigEndian.PutUint16(message[0:2], stunBindingRequest)
	binary.BigEndian.PutUint16(message[2:4], 0)
	binary.BigEndian.PutUint32(message[4:8], stunMagicCookie)
	copy(message[8:stunHeaderSize], transactionID[:])
	return message
}

type stunAttribute struct {
	Type  uint16
	Value []byte
}

// parseBindingResponse 解析 STUN 响应，返回 XOR-MAPPED-ADDRESS 里的地址与端口。
func parseBindingResponse(payload []byte, transactionID [stunTransactionIDSize]byte) (netip.Addr, uint16, error) {
	if len(payload) < stunHeaderSize {
		return netip.Addr{}, 0, errors.New("响应不足 20 字节")
	}
	messageType := binary.BigEndian.Uint16(payload[0:2])
	length := int(binary.BigEndian.Uint16(payload[2:4]))
	cookie := binary.BigEndian.Uint32(payload[4:8])
	if cookie != stunMagicCookie {
		return netip.Addr{}, 0, errors.New("魔数不匹配（不是 STUN 响应）")
	}
	if !bytes.Equal(payload[8:stunHeaderSize], transactionID[:]) {
		return netip.Addr{}, 0, errors.New("事务 ID 不匹配")
	}
	if messageType == stunBindingError {
		return netip.Addr{}, 0, errors.New("STUN 返回错误响应")
	}
	if messageType != stunBindingSuccess {
		return netip.Addr{}, 0, fmt.Errorf("未知的 STUN 消息类型 0x%04x", messageType)
	}
	body := payload[stunHeaderSize:]
	if length > len(body) {
		return netip.Addr{}, 0, errors.New("响应长度字段越界")
	}
	attributes, err := parseAttributes(body[:length])
	if err != nil {
		return netip.Addr{}, 0, err
	}
	for _, attribute := range attributes {
		switch attribute.Type {
		case stunAttrXORMappedAddress:
			return parseXORMappedAddress(attribute.Value, transactionID)
		case stunAttrMappedAddress:
			// 老服务器只给 MAPPED-ADDRESS（非 XOR），能用就用。
			return parseMappedAddress(attribute.Value)
		}
	}
	return netip.Addr{}, 0, errors.New("响应里没有 XOR-MAPPED-ADDRESS")
}

// parseAttributes 解析属性列表（每个属性按 4 字节对齐填充）。
func parseAttributes(body []byte) ([]stunAttribute, error) {
	attributes := make([]stunAttribute, 0, 4)
	for offset := 0; offset+4 <= len(body); {
		attributeType := binary.BigEndian.Uint16(body[offset : offset+2])
		length := int(binary.BigEndian.Uint16(body[offset+2 : offset+4]))
		offset += 4
		if offset+length > len(body) {
			return attributes, errors.New("STUN 属性越界")
		}
		attributes = append(attributes, stunAttribute{Type: attributeType, Value: body[offset : offset+length]})
		offset += length
		if padding := length % 4; padding != 0 {
			offset += 4 - padding
		}
	}
	return attributes, nil
}

// parseXORMappedAddress 解析 XOR-MAPPED-ADDRESS（RFC 5389 §15.2）。
//
// 端口与地址都与魔 cookie（以及 IPv6 时的 transaction ID）做异或。
func parseXORMappedAddress(value []byte, transactionID [stunTransactionIDSize]byte) (netip.Addr, uint16, error) {
	if len(value) < 4 {
		return netip.Addr{}, 0, errors.New("XOR-MAPPED-ADDRESS 长度不足")
	}
	family := value[1]
	port := binary.BigEndian.Uint16(value[2:4]) ^ uint16(stunMagicCookie>>16)
	switch family {
	case 0x01:
		if len(value) < 8 {
			return netip.Addr{}, 0, errors.New("XOR-MAPPED-ADDRESS 的 IPv4 长度不足")
		}
		var raw [4]byte
		copy(raw[:], value[4:8])
		var mask [4]byte
		binary.BigEndian.PutUint32(mask[:], stunMagicCookie)
		for index := range raw {
			raw[index] ^= mask[index]
		}
		return netip.AddrFrom4(raw), port, nil
	case 0x02:
		if len(value) < 20 {
			return netip.Addr{}, 0, errors.New("XOR-MAPPED-ADDRESS 的 IPv6 长度不足")
		}
		var mask [16]byte
		binary.BigEndian.PutUint32(mask[0:4], stunMagicCookie)
		copy(mask[4:], transactionID[:])
		var raw [16]byte
		copy(raw[:], value[4:20])
		for index := range raw {
			raw[index] ^= mask[index]
		}
		return netip.AddrFrom16(raw), port, nil
	default:
		return netip.Addr{}, 0, fmt.Errorf("不支持的地址族 0x%02x", family)
	}
}

// parseMappedAddress 解析非 XOR 的 MAPPED-ADDRESS（老服务器的兼容路径）。
func parseMappedAddress(value []byte) (netip.Addr, uint16, error) {
	if len(value) < 4 {
		return netip.Addr{}, 0, errors.New("MAPPED-ADDRESS 长度不足")
	}
	family := value[1]
	port := binary.BigEndian.Uint16(value[2:4])
	switch family {
	case 0x01:
		if len(value) < 8 {
			return netip.Addr{}, 0, errors.New("MAPPED-ADDRESS 的 IPv4 长度不足")
		}
		var raw [4]byte
		copy(raw[:], value[4:8])
		return netip.AddrFrom4(raw), port, nil
	case 0x02:
		if len(value) < 20 {
			return netip.Addr{}, 0, errors.New("MAPPED-ADDRESS 的 IPv6 长度不足")
		}
		var raw [16]byte
		copy(raw[:], value[4:20])
		return netip.AddrFrom16(raw), port, nil
	default:
		return netip.Addr{}, 0, fmt.Errorf("不支持的地址族 0x%02x", family)
	}
}
