// Package relay 实现 NEILICO 的中继（relay）服务端。
//
// 背景：两个节点都在不可达 NAT 后面时，UDP 打洞失败，WireGuard 隧道建不起来。
// 中继把两边的 WireGuard 报文在服务器上转发一遍，隧道就能跑起来——代价是流量
// 全部过服务器（带宽受限、延迟增加）。
//
// 帧格式（小端，自己定义的简单格式）：
//
//	byte 0      : frameType（1=REGISTER, 2=KEEPALIVE, 3=DATA, 4=REGISTERED, 5=ERROR）
//	bytes 1..17 : session/node id（16 字节 UUID）
//	bytes 17..  : payload
//
//	- REGISTER   客户端 → 服务端：头里的 node id 由客户端填，payload 是
//	              token_len(2 字节) + token。
//	- REGISTERED 服务端 → 客户端：头里的 node id 回填，payload 是 public_host:port。
//	- KEEPALIVE  客户端 → 服务端：只刷新会话的存活时间。
//	- DATA       双向：payload 前 16 字节是目标 node id，其后是原始 WireGuard
//	              报文（不解析、不修改）。服务端把头里的 node id 回填成**源**节点 id，
//	              接收方据此知道该把报文交给哪个本地代理。
//	- ERROR      服务端 → 客户端：鉴权/路由失败。**不泄露具体原因**。
package relay

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

const (
	// FrameRegister 是客户端登记会话的帧。
	FrameRegister byte = 1
	// FrameKeepalive 是客户端的保活帧。
	FrameKeepalive byte = 2
	// FrameData 是承载 WireGuard 报文的帧。
	FrameData byte = 3
	// FrameRegistered 是服务端登记成功的回执。
	FrameRegistered byte = 4
	// FrameError 是服务端的错误回执（不泄露原因）。
	FrameError byte = 5
)

const (
	// headerSize = 1 字节帧类型 + 16 字节 node id。
	headerSize = 17
	// nodeIDSize 是 node id（UUID）的字节数。
	nodeIDSize = 16
	// maxFrameSize 是单个 UDP 帧的上限（WireGuard 报文最大 65535 字节 + 头部）。
	maxFrameSize = 64*1024 + headerSize
)

// ErrShortFrame 表示收到的数据不足以构成一个完整帧头。
var ErrShortFrame = errors.New("relay: 帧长度不足")

// Frame 是一个中继协议帧。
type Frame struct {
	// Type 是帧类型（FrameRegister 等）。
	Type byte
	// NodeID 是会话/节点 id（UUID）。
	NodeID uuid.UUID
	// Payload 是帧载荷。
	Payload []byte
}

// Encode 把帧序列化成字节（头 + 载荷）。
func (f Frame) Encode() []byte {
	buffer := make([]byte, headerSize+len(f.Payload))
	buffer[0] = f.Type
	copy(buffer[1:headerSize], f.NodeID[:])
	copy(buffer[headerSize:], f.Payload)
	return buffer
}

// DecodeFrame 解析一个中继帧。
func DecodeFrame(buffer []byte) (Frame, error) {
	if len(buffer) < headerSize {
		return Frame{}, fmt.Errorf("%w: 至少需要 %d 字节，实际 %d", ErrShortFrame, headerSize, len(buffer))
	}
	var nodeID uuid.UUID
	copy(nodeID[:], buffer[1:headerSize])
	payload := make([]byte, len(buffer)-headerSize)
	copy(payload, buffer[headerSize:])
	return Frame{Type: buffer[0], NodeID: nodeID, Payload: payload}, nil
}

// EncodeToken 编码 REGISTER 帧的 token 载荷：token_len(2 字节小端) + token。
func EncodeToken(token string) []byte {
	buffer := make([]byte, 2+len(token))
	binary.LittleEndian.PutUint16(buffer, uint16(len(token)))
	copy(buffer[2:], token)
	return buffer
}

// DecodeToken 解析 REGISTER 帧的 token 载荷。
func DecodeToken(payload []byte) (string, error) {
	if len(payload) < 2 {
		return "", errors.New("relay: token 载荷长度不足")
	}
	length := int(binary.LittleEndian.Uint16(payload[:2]))
	if length > len(payload)-2 {
		return "", errors.New("relay: token 长度越界")
	}
	return string(payload[2 : 2+length]), nil
}

// EncodeData 编码 DATA 帧载荷：目标 node id（16 字节）+ 原始 WireGuard 报文。
func EncodeData(destination uuid.UUID, packet []byte) []byte {
	payload := make([]byte, nodeIDSize+len(packet))
	copy(payload[:nodeIDSize], destination[:])
	copy(payload[nodeIDSize:], packet)
	return payload
}

// DecodeData 解析 DATA 帧载荷，返回目标 node id 与原始 WireGuard 报文。
func DecodeData(payload []byte) (uuid.UUID, []byte, error) {
	if len(payload) < nodeIDSize {
		return uuid.Nil, nil, errors.New("relay: DATA 载荷长度不足")
	}
	var destination uuid.UUID
	copy(destination[:], payload[:nodeIDSize])
	packet := make([]byte, len(payload)-nodeIDSize)
	copy(packet, payload[nodeIDSize:])
	return destination, packet, nil
}
