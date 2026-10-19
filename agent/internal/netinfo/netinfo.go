package netinfo

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Info 描述 Agent 所在的网络环境，用于控制面判断「多台节点是否处于同一个实体局域网」。
//
// 为什么需要它：私网段（192.168.1.0/24）在不同场地大量重复，而 Docker bridge /
// VM NAT 里看到的网段根本不是宿主机局域网。只看网段会把两个不同办公室误判成主备；
// 真正能代表「同一个二层广播域」的是**默认网关的 MAC**（同一台路由器）。
type Info struct {
	// Environment: host | container | vm | unknown
	Environment string `json:"environment"`
	// GatewayIP / GatewayMAC: 默认路由的网关与其 ARP 地址（拿不到时为空）。
	GatewayIP  string `json:"gateway_ip,omitempty"`
	GatewayMAC string `json:"gateway_mac,omitempty"`
	// GatewayOnPhysical 表示默认路由走的是物理网卡（不是 docker bridge / veth / tun）。
	// 只有它为 true 时，网关 MAC 才能代表宿主机所在的真实局域网。
	GatewayOnPhysical bool `json:"gateway_on_physical"`
}

// Detect 采集当前环境。任何一步失败都不阻断心跳，只留空字段。
func Detect() Info {
	return DetectWith(probes{
		GOOS:       runtime.GOOS,
		ReadFile:   os.ReadFile,
		StatExists: exists,
		Run:        run,
	})
}

type probes struct {
	GOOS       string
	ReadFile   func(string) ([]byte, error)
	StatExists func(string) bool
	Run        func(name string, args ...string) (string, error)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

func DetectWith(p probes) Info {
	info := Info{Environment: "unknown"}
	switch p.GOOS {
	case "linux":
		info.Environment = detectLinuxEnvironment(p)
		gatewayIP, iface := detectDefaultRoute(p)
		info.GatewayIP = gatewayIP
		if iface != "" {
			// 只有默认路由走物理网卡时，网关 MAC 才代表真实局域网；
			// docker0/veth/tun 之类的虚拟接口一律不信。
			info.GatewayOnPhysical = p.StatExists("/sys/class/net/" + iface + "/device")
		}
		if gatewayIP != "" {
			info.GatewayMAC = lookupNeighborMAC(p, gatewayIP)
		}
	case "darwin":
		info.Environment = "host"
		if out, err := p.Run("route", "-n", "get", "default"); err == nil {
			info.GatewayIP = parseDarwinGateway(out)
		}
	case "windows":
		info.Environment = "host"
	}
	return info
}

func detectLinuxEnvironment(p probes) string {
	if p.StatExists("/.dockerenv") || p.StatExists("/run/.containerenv") {
		return "container"
	}
	if raw, err := p.ReadFile("/proc/1/cgroup"); err == nil {
		text := string(raw)
		if strings.Contains(text, "docker") || strings.Contains(text, "kubepods") || strings.Contains(text, "containerd") {
			return "container"
		}
	}
	for _, path := range []string{"/sys/class/dmi/id/product_name", "/sys/class/dmi/id/sys_vendor"} {
		if raw, err := p.ReadFile(path); err == nil {
			value := strings.ToLower(string(raw))
			for _, marker := range []string{"kvm", "qemu", "vmware", "virtualbox", "hyper-v", "virtual machine", "bochs"} {
				if strings.Contains(value, marker) {
					return "vm"
				}
			}
		}
	}
	return "host"
}

// detectDefaultRoute 解析 `ip route show default`，返回网关与出接口。
func detectDefaultRoute(p probes) (gateway, iface string) {
	out, err := p.Run("ip", "route", "show", "default")
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		for i := 0; i < len(fields)-1; i++ {
			switch fields[i] {
			case "via":
				gateway = fields[i+1]
			case "dev":
				iface = fields[i+1]
			}
		}
		if gateway != "" && iface != "" {
			return gateway, iface
		}
	}
	return gateway, iface
}

// lookupNeighborMAC 从 ARP/邻居表里取网关 MAC（需要近期有流量，拿不到就留空）。
func lookupNeighborMAC(p probes, gateway string) string {
	out, err := p.Run("ip", "neigh", "show", gateway)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		for i := 0; i < len(fields)-1; i++ {
			if fields[i] == "lladdr" {
				return strings.ToLower(fields[i+1])
			}
		}
	}
	return ""
}

func parseDarwinGateway(output string) string {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "gateway:" {
			return fields[1]
		}
	}
	return ""
}
