package webproxy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
)

// Site 是一条待下发的反代规则。
type Site struct {
	Domain    string `json:"domain"`
	Target    string `json:"target"` // host:port（NEILICO 内置反代后面的目标）
	TLS       bool   `json:"tls"`    // 由外部已有证书终止时置 true，仅影响是否生成 80 段
	WebSocket bool   `json:"websocket"`
}

// Plan 是一次「生成 + 校验」的结果，供 dry-run 预览。
type Plan struct {
	Candidate  Candidate `json:"candidate"`
	Domain     string    `json:"domain"`
	Path       string    `json:"path"`
	Content    string    `json:"content"`
	Validated  bool      `json:"validated"`
	Validation string    `json:"validation,omitempty"`
	Applied    bool      `json:"applied"`
	Reloaded   bool      `json:"reloaded"`
	RolledBack bool      `json:"rolled_back,omitempty"`
}

// ErrNotWritable 表示目标代理不可写（没挂载 / 没有开启写权限）。
var ErrNotWritable = errors.New("web proxy is not writable: mount its config dir and set NEILICO_WEBPROXY_WRITE=1")

// Render 按代理类型生成站点配置。
func Render(c Candidate, s Site) (string, error) {
	if strings.TrimSpace(s.Domain) == "" {
		return "", errors.New("domain is required")
	}
	if strings.TrimSpace(s.Target) == "" {
		return "", errors.New("target is required")
	}
	switch c.Kind {
	case KindNginx:
		return renderNginx(s), nil
	case KindCaddy:
		return renderCaddy(s), nil
	case KindTraefik:
		return "", errors.New("traefik uses dynamic labels; file rendering is not supported yet")
	default:
		return "", fmt.Errorf("unsupported proxy kind %q", c.Kind)
	}
}

func renderNginx(s Site) string {
	var b strings.Builder
	marker := "# managed by NEILICO — " + s.Domain
	b.WriteString(marker + "\n")
	b.WriteString("server {\n")
	b.WriteString("    listen 80;\n")
	b.WriteString("    server_name " + s.Domain + ";\n")
	b.WriteString("    client_max_body_size 512m;\n\n")
	b.WriteString("    location / {\n")
	b.WriteString("        proxy_pass http://" + s.Target + ";\n")
	b.WriteString("        proxy_http_version 1.1;\n")
	b.WriteString("        proxy_set_header Host $host;\n")
	b.WriteString("        proxy_set_header X-Real-IP $remote_addr;\n")
	b.WriteString("        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n")
	b.WriteString("        proxy_set_header X-Forwarded-Proto $scheme;\n")
	if s.WebSocket {
		b.WriteString("        proxy_set_header Upgrade $http_upgrade;\n")
		b.WriteString("        proxy_set_header Connection \"upgrade\";\n")
	}
	b.WriteString("        proxy_buffering off;\n")
	b.WriteString("        proxy_read_timeout 3600s;\n")
	b.WriteString("    }\n")
	b.WriteString("}\n")
	if s.TLS {
		b.WriteString("\n# TLS: 由宿主机已有证书终止；certbot/宝塔 会在这个 server 块上补 443 段。\n")
	}
	return b.String()
}

func renderCaddy(s Site) string {
	var b strings.Builder
	b.WriteString("# managed by NEILICO — " + s.Domain + "\n")
	b.WriteString(s.Domain + " {\n")
	b.WriteString("\tencode zstd gzip\n")
	b.WriteString("\treverse_proxy " + s.Target + "\n")
	b.WriteString("}\n")
	return b.String()
}

// Plan 生成配置并做语法校验；不给 dryRun 之外的选择，写盘由 Apply 负责。
func Preview(ctx context.Context, c Candidate, s Site) (Plan, error) {
	content, err := Render(c, s)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Candidate: c, Domain: s.Domain, Content: content, Path: ManagedPath(c, s.Domain)}
	if c.Kind == KindTraefik {
		return p, nil
	}
	valid, output, verr := validateContent(ctx, c, p.Path, content)
	p.Validated, p.Validation = valid, output
	if verr != nil && !valid {
		return p, nil // 校验失败属于「预览结论」，不是内部错误
	}
	return p, nil
}

// Apply 写盘 → 校验 → （失败则回滚）→ 热重载。
// dryRun=true 时只写临时文件校验，然后删除，不改动线上配置。
func Apply(ctx context.Context, c Candidate, s Site, dryRun bool) (Plan, error) {
	p, err := Preview(ctx, c, s)
	if err != nil {
		return p, err
	}
	if c.Kind == KindTraefik {
		return p, errors.New("traefik 暂不支持文件下发")
	}
	if !c.Writable {
		return p, ErrNotWritable
	}
	if !p.Validated {
		return p, fmt.Errorf("config validation failed: %s", p.Validation)
	}

	if dryRun {
		cleanupContent(ctx, c, p.Path)
		p.RolledBack = true
		return p, nil
	}

	valid, output, err := writeAndValidate(ctx, c, p.Path, p.Content)
	p.Validated, p.Validation = valid, output
	if err != nil || !valid {
		cleanupContent(ctx, c, p.Path)
		return p, fmt.Errorf("write/validate failed: %s", strings.TrimSpace(output))
	}
	p.Applied = true
	if err := Reload(ctx, c); err != nil {
		cleanupContent(ctx, c, p.Path)
		return p, fmt.Errorf("reload failed: %w", err)
	}
	p.Reloaded = true
	return p, nil
}

// Reload 触发热重载。
func Reload(ctx context.Context, c Candidate) error {
	var cmd *exec.Cmd
	switch c.Kind {
	case KindNginx:
		if c.Runtime == RuntimeDocker {
			cmd = exec.CommandContext(ctx, "docker", "exec", c.Name, "nginx", "-s", "reload")
		} else {
			cmd = exec.CommandContext(ctx, "nginx", "-s", "reload")
		}
	case KindCaddy:
		if c.Runtime == RuntimeDocker {
			cmd = exec.CommandContext(ctx, "docker", "exec", c.Name, "caddy", "reload", "--config", "/etc/caddy/Caddyfile")
		} else {
			cmd = exec.CommandContext(ctx, "caddy", "reload", "--config", "/etc/caddy/Caddyfile")
		}
	default:
		return fmt.Errorf("reload not supported for %q", c.Kind)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func ManagedPath(c Candidate, domain string) string {
	name := "neilico-" + sanitizeDomain(domain)
	switch c.Kind {
	case KindNginx:
		dir := c.ConfigDir
		if dir == "" {
			dir = "/etc/nginx/conf.d"
		}
		return path.Join(dir, name+".conf")
	case KindCaddy:
		dir := c.ConfigDir
		if dir == "" {
			dir = "/etc/caddy"
		}
		return path.Join(dir, name+".caddy")
	}
	return ""
}

func sanitizeDomain(domain string) string {
	repl := strings.NewReplacer("*", "wildcard", "/", "-", ":", "-", " ", "")
	return repl.Replace(strings.ToLower(domain))
}

// validateContent 用临时文件做语法检查（native 直接写目标目录，docker 用 exec 写入）。
func validateContent(ctx context.Context, c Candidate, targetPath, content string) (bool, string, error) {
	if c.Runtime == RuntimeDocker {
		tmp := targetPath + ".neilico-tmp"
		if err := dockerWrite(ctx, c.Name, tmp, content); err != nil {
			return false, err.Error(), err
		}
		valid, out := dockerValidate(ctx, c, tmp)
		_ = dockerRemove(ctx, c.Name, tmp)
		return valid, out, nil
	}
	tmp := targetPath + ".neilico-tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return false, err.Error(), err
	}
	valid, out := nativeValidate(ctx, c, tmp)
	_ = os.Remove(tmp)
	return valid, out, nil
}

func writeAndValidate(ctx context.Context, c Candidate, targetPath, content string) (bool, string, error) {
	if c.Runtime == RuntimeDocker {
		if err := dockerWrite(ctx, c.Name, targetPath, content); err != nil {
			return false, err.Error(), err
		}
		valid, out := dockerValidate(ctx, c, targetPath)
		return valid, out, nil
	}
	if err := os.WriteFile(targetPath, []byte(content), 0o644); err != nil {
		return false, err.Error(), err
	}
	valid, out := nativeValidate(ctx, c, targetPath)
	return valid, out, nil
}

func cleanupContent(ctx context.Context, c Candidate, targetPath string) {
	if c.Runtime == RuntimeDocker {
		_ = dockerRemove(ctx, c.Name, targetPath)
		return
	}
	_ = os.Remove(targetPath)
}

func nativeValidate(ctx context.Context, c Candidate, file string) (bool, string) {
	var cmd *exec.Cmd
	switch c.Kind {
	case KindNginx:
		cmd = exec.CommandContext(ctx, "nginx", "-t")
	case KindCaddy:
		cmd = exec.CommandContext(ctx, "caddy", "validate", "--config", file)
	default:
		return false, "unsupported"
	}
	out, err := cmd.CombinedOutput()
	return err == nil, strings.TrimSpace(string(out))
}

func dockerValidate(ctx context.Context, c Candidate, file string) (bool, string) {
	var cmd *exec.Cmd
	switch c.Kind {
	case KindNginx:
		cmd = exec.CommandContext(ctx, "docker", "exec", c.Name, "nginx", "-t")
	case KindCaddy:
		cmd = exec.CommandContext(ctx, "docker", "exec", c.Name, "caddy", "validate", "--config", file)
	default:
		return false, "unsupported"
	}
	out, err := cmd.CombinedOutput()
	return err == nil, strings.TrimSpace(string(out))
}

func dockerWrite(ctx context.Context, container, path, content string) error {
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "sh", "-c", "cat > "+path)
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker write %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func dockerRemove(ctx context.Context, container, path string) error {
	return exec.CommandContext(ctx, "docker", "exec", container, "rm", "-f", path).Run()
}
