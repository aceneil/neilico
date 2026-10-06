//go:build !linux

package capabilities

// 非 Linux 平台没有可通过 netlink 直接创建的内核 WireGuard 接口（Windows/macOS
// 走各自的驱动/系统组件），因此不接线真实探测，detectTunnel 会退化为静态/客户端
// 判断。保持 nil 而不是返回固定错误，是为了让上层清楚地「没有探测」而非「探测失败」。
var defaultInterfaceProbe func() error
