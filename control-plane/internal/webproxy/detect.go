// Package webproxy 负责识别宿主机上已有的 Web 反向代理（Nginx / Caddy /
// Traefik，Docker 或系统原生），并按 NEILICO 的域名规则生成站点配置、
// 做语法校验、热重载。
//
// 设计约束：
//   - 控制面跑在容器里，默认只能看到容器自身。要在容器里管理宿主代理，
//     必须显式挂载宿主的配置目录 / docker.sock；Detect 会把「能不能写」
//     如实报出来，不会假装成功。
//   - 所有会产生副作用的操作（写文件、reload）都必须显式开启：
//     NEILICO_WEBPROXY_WRITE=1，且请求里 dry_run=false。
package webproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Kind 是反向代理类型。
type Kind string

const (
	KindNginx   Kind = "nginx"
	KindCaddy   Kind = "caddy"
	KindTraefik Kind = "traefik"
)

// Runtime 表示它跑在容器里还是宿主机上。
type Runtime string

const (
	RuntimeNative Runtime = "native"
	RuntimeDocker Runtime = "docker"
)

// Candidate 是一个可管理的反向代理实例。
type Candidate struct {
	Kind      Kind     `json:"kind"`
	Runtime   Runtime  `json:"runtime"`
	Name      string   `json:"name"` // 容器名；原生为 "system"
	Image     string   `json:"image,omitempty"`
	Binary    string   `json:"binary,omitempty"`
	ConfigDir string   `json:"config_dir,omitempty"` // nginx 的 conf.d / caddy 的 Caddyfile 目录
	Writable  bool     `json:"writable"`
	Notes     []string `json:"notes,omitempty"`
}

// Result 是探测结果。
type Result struct {
	Candidates []Candidate `json:"candidates"`
	HostAccess bool        `json:"host_access"` // 容器里能否看到宿主配置（决定 writable）
	Hints      []string    `json:"hints,omitempty"`
}

// Options 允许测试时注入探针。
type Options struct {
	LookPath     func(string) (string, error)
	Stat         func(string) (os.FileInfo, error)
	Run          func(ctx context.Context, name string, args ...string) (string, error)
	WriteGate    bool   // NEILICO_WEBPROXY_WRITE
	DockerSocket string // 空 = 用默认 /var/run/docker.sock；测试可指向不存在的路径
}

func defaultOptions() Options {
	return Options{
		LookPath: exec.LookPath,
		Stat:     os.Stat,
		Run: func(ctx context.Context, name string, args ...string) (string, error) {
			out, err := exec.CommandContext(ctx, name, args...).Output()
			return string(out), err
		},
		WriteGate:    os.Getenv("NEILICO_WEBPROXY_WRITE") == "1",
		DockerSocket: dockerSocketPath(),
	}
}

// Detect 探测本机可管理的反向代理。
func Detect(ctx context.Context) Result {
	return DetectWith(ctx, defaultOptions())
}

// DetectWith 是带注入探针的实现，便于测试。
func DetectWith(ctx context.Context, opts Options) Result {
	res := Result{Candidates: []Candidate{}}

	// 1) 原生 Nginx
	if bin, err := opts.LookPath("nginx"); err == nil {
		if _, statErr := opts.Stat("/etc/nginx/nginx.conf"); statErr == nil {
			c := Candidate{Kind: KindNginx, Runtime: RuntimeNative, Name: "system", Binary: bin,
				ConfigDir: "/etc/nginx/conf.d"}
			c.Writable = opts.WriteGate && dirWritable("/etc/nginx/conf.d")
			res.Candidates = append(res.Candidates, c)
		}
	}
	// 2) 原生 Caddy
	if bin, err := opts.LookPath("caddy"); err == nil {
		if _, statErr := opts.Stat("/etc/caddy/Caddyfile"); statErr == nil {
			c := Candidate{Kind: KindCaddy, Runtime: RuntimeNative, Name: "system", Binary: bin,
				ConfigDir: "/etc/caddy"}
			c.Writable = opts.WriteGate && fileWritable("/etc/caddy/Caddyfile")
			res.Candidates = append(res.Candidates, c)
		}
	}

	// 3) Docker 里的代理：优先直接读 docker.sock（镜像里不需要装 docker CLI）
	socket := opts.DockerSocket
	if socket == "" {
		socket = dockerSocketPath()
	}
	network, addr, baseURL := dockerEndpoint(socket)
	if list, err := dockerListAt(ctx, network, addr, baseURL); err == nil {
		for _, c := range candidatesFromDocker(list, opts.WriteGate) {
			res.Candidates = append(res.Candidates, c)
		}
	} else if socketAvailable(socket) {
		// socket 存在但调用失败：把原因报出来，别让用户对着空列表猜
		res.Hints = append(res.Hints, "读取 docker.sock 失败："+err.Error())
		if strings.Contains(err.Error(), "permission denied") {
			res.Hints = append(res.Hints,
				"服务以非 root 用户运行，读不到 root:docker 的 socket：在 compose 的 server 服务上加 group_add: [\"<socket 的 GID>\"]（宿主执行 stat -c %g /var/run/docker.sock 查看）。")
		}
	}
	if _, err := opts.LookPath("docker"); err == nil {
		out, runErr := opts.Run(ctx, "docker", "ps", "--format", "{{.Names}}\t{{.Image}}\t{{.Ports}}")
		if runErr == nil {
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				fields := strings.Split(line, "\t")
				if len(fields) < 2 {
					continue
				}
				name, image := fields[0], fields[1]
				kind, ok := kindFromImage(image)
				if !ok {
					continue
				}
				c := Candidate{Kind: kind, Runtime: RuntimeDocker, Name: name, Image: image}
				if len(fields) > 2 {
					c.Notes = append(c.Notes, "ports: "+fields[2])
				}
				c.Writable = opts.WriteGate
				res.Candidates = append(res.Candidates, c)
			}
		}
	}

	res.HostAccess = len(res.Candidates) > 0
	if len(res.Candidates) == 0 {
		res.Hints = append(res.Hints,
			"未发现可管理的反向代理。若宿主上有 Nginx/Caddy，请在 compose 里挂载其配置目录与 docker.sock 后再试。")
	}
	if !opts.WriteGate {
		res.Hints = append(res.Hints,
			"当前为只读探测：设置 NEILICO_WEBPROXY_WRITE=1 才允许写配置与重载。")
	}
	return res
}

// kindFromImage 从镜像名判断代理类型。
func kindFromImage(image string) (Kind, bool) {
	img := strings.ToLower(image)
	switch {
	case strings.Contains(img, "nginx"):
		return KindNginx, true
	case strings.Contains(img, "caddy"):
		return KindCaddy, true
	case strings.Contains(img, "traefik"):
		return KindTraefik, true
	}
	return "", false
}

func dirWritable(path string) bool {
	f, err := os.CreateTemp(path, ".neilico-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

func fileWritable(path string) bool {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// ── Docker 探测（走 socket，不依赖 CLI）────────────────────────────────────

type dockerContainer struct {
	Names []string `json:"Names"`
	Image string   `json:"Image"`
	State string   `json:"State"`
	Ports []struct {
		PublicPort  int    `json:"PublicPort"`
		PrivatePort int    `json:"PrivatePort"`
		Type        string `json:"Type"`
	} `json:"Ports"`
}

// dockerSocketPath 返回 Docker 端点：DOCKER_HOST 优先（unix:// 或 tcp://），否则默认 socket。
func dockerSocketPath() string {
	if v := strings.TrimSpace(os.Getenv("DOCKER_HOST")); v != "" {
		return v
	}
	return "/var/run/docker.sock"
}

// dockerEndpoint 把端点字符串拆成 (network, addr, baseURL)。
//
//	unix:///var/run/docker.sock  → unix + 路径
//	tcp://proxy:2375             → tcp  + host:port（docker-socket-proxy 场景）
//	http://proxy:2375            → tcp
func dockerEndpoint(raw string) (network, addr, baseURL string) {
	v := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(v, "unix://"):
		return "unix", strings.TrimPrefix(v, "unix://"), "http://docker"
	case strings.HasPrefix(v, "tcp://"):
		host := strings.TrimPrefix(v, "tcp://")
		return "tcp", host, "http://" + host
	case strings.HasPrefix(v, "http://"), strings.HasPrefix(v, "https://"):
		host := strings.TrimPrefix(strings.TrimPrefix(v, "http://"), "https://")
		return "tcp", host, v
	default:
		return "unix", v, "http://docker"
	}
}

// dockerListAt 调 Docker Engine API 的 /containers/json。
func dockerListAt(ctx context.Context, network, addr, baseURL string) ([]dockerContainer, error) {
	if network == "unix" {
		if _, err := os.Stat(addr); err != nil {
			return nil, err
		}
	} else if strings.TrimSpace(addr) == "" {
		return nil, fmt.Errorf("docker endpoint %q is empty", addr)
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/containers/json", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("docker api %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var list []dockerContainer
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	return list, nil
}

func candidatesFromDocker(list []dockerContainer, writeGate bool) []Candidate {
	out := make([]Candidate, 0, len(list))
	for _, item := range list {
		kind, ok := kindFromImage(item.Image)
		if !ok {
			continue
		}
		name := strings.TrimPrefix(firstOr(item.Names, ""), "/")
		c := Candidate{Kind: kind, Runtime: RuntimeDocker, Name: name, Image: item.Image, Writable: writeGate}
		if ports := formatPorts(item); ports != "" {
			c.Notes = append(c.Notes, "ports: "+ports)
		}
		out = append(out, c)
	}
	return out
}

func formatPorts(item dockerContainer) string {
	var parts []string
	for _, p := range item.Ports {
		if p.PublicPort > 0 {
			parts = append(parts, fmt.Sprintf("%d->%d/%s", p.PublicPort, p.PrivatePort, p.Type))
		}
	}
	return strings.Join(parts, ",")
}

func firstOr(values []string, fallback string) string {
	if len(values) > 0 && values[0] != "" {
		return values[0]
	}
	return fallback
}

// socketAvailable 只判断路径是否存在（用于区分「没挂载」和「调不通」）。
func socketAvailable(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
