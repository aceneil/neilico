package webproxy

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRenderNginxIncludesDomainAndTarget(t *testing.T) {
	content, err := Render(Candidate{Kind: KindNginx}, Site{Domain: "app.example.com", Target: "127.0.0.1:8081"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"server_name app.example.com;", "proxy_pass http://127.0.0.1:8081;", "managed by NEILICO"} {
		if !strings.Contains(content, want) {
			t.Fatalf("nginx config missing %q:\n%s", want, content)
		}
	}
}

func TestRenderCaddyIncludesDomainAndTarget(t *testing.T) {
	content, err := Render(Candidate{Kind: KindCaddy}, Site{Domain: "app.example.com", Target: "127.0.0.1:8081"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(content, "app.example.com {") || !strings.Contains(content, "reverse_proxy 127.0.0.1:8081") {
		t.Fatalf("caddy config wrong:\n%s", content)
	}
}

func TestRenderRejectsTraefikAndEmptyInput(t *testing.T) {
	if _, err := Render(Candidate{Kind: KindTraefik}, Site{Domain: "a.test", Target: "127.0.0.1:1"}); err == nil {
		t.Fatal("expected traefik render error")
	}
	if _, err := Render(Candidate{Kind: KindNginx}, Site{Domain: "", Target: "127.0.0.1:1"}); err == nil {
		t.Fatal("expected domain validation error")
	}
}

func TestKindFromImage(t *testing.T) {
	cases := map[string]Kind{
		"nginx:alpine":           KindNginx,
		"library/nginx:1.27":     KindNginx,
		"caddy:2-alpine":         KindCaddy,
		"traefik:v3":             KindTraefik,
		"aceneil/neilico-server": "",
	}
	for image, want := range cases {
		got, ok := kindFromImage(image)
		if want == "" {
			if ok {
				t.Fatalf("%s should not be a proxy, got %s", image, got)
			}
			continue
		}
		if !ok || got != want {
			t.Fatalf("%s => %q (%v), want %q", image, got, ok, want)
		}
	}
}

func TestDetectFindsDockerProxy(t *testing.T) {
	opts := Options{
		LookPath: func(name string) (string, error) {
			if name == "docker" {
				return "/usr/bin/docker", nil
			}
			return "", errors.New("not found")
		},
		Stat: func(string) (os.FileInfo, error) { return nil, errors.New("missing") },
		Run: func(ctx context.Context, name string, args ...string) (string, error) {
			return "fanqie-web\tnginx:alpine\t0.0.0.0:9180->80/tcp\nworker\tredis:7\t6379/tcp\n", nil
		},
		WriteGate:    false,
		DockerSocket: "/nonexistent/docker.sock",
	}
	res := DetectWith(context.Background(), opts)
	if len(res.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d: %#v", len(res.Candidates), res.Candidates)
	}
	c := res.Candidates[0]
	if c.Kind != KindNginx || c.Runtime != RuntimeDocker || c.Name != "fanqie-web" {
		t.Fatalf("unexpected candidate: %#v", c)
	}
	if c.Writable {
		t.Fatal("must not be writable without the write gate")
	}
	if !hasHint(res.Hints, "只读探测") {
		t.Fatalf("expected read-only hint, got %#v", res.Hints)
	}
}

func TestDetectNativeNginx(t *testing.T) {
	opts := Options{
		LookPath: func(name string) (string, error) {
			if name == "nginx" {
				return "/usr/sbin/nginx", nil
			}
			return "", errors.New("not found")
		},
		Stat: func(path string) (os.FileInfo, error) {
			if path == "/etc/nginx/nginx.conf" {
				return fakeInfo{}, nil
			}
			return nil, errors.New("missing")
		},
		Run:          func(ctx context.Context, name string, args ...string) (string, error) { return "", nil },
		WriteGate:    false,
		DockerSocket: "/nonexistent/docker.sock",
	}
	res := DetectWith(context.Background(), opts)
	if len(res.Candidates) != 1 || res.Candidates[0].Runtime != RuntimeNative {
		t.Fatalf("expected native nginx, got %#v", res.Candidates)
	}
}

func TestManagedPath(t *testing.T) {
	got := ManagedPath(Candidate{Kind: KindNginx, ConfigDir: "/etc/nginx/conf.d"}, "*.Example.COM")
	if got != "/etc/nginx/conf.d/neilico-wildcard.example.com.conf" {
		t.Fatalf("unexpected path: %s", got)
	}
}

func TestApplyRefusesWhenNotWritable(t *testing.T) {
	_, err := Apply(context.Background(), Candidate{Kind: KindNginx, Runtime: RuntimeNative, ConfigDir: t.TempDir()}, Site{Domain: "a.test", Target: "127.0.0.1:1"}, true)
	if !errors.Is(err, ErrNotWritable) {
		t.Fatalf("expected ErrNotWritable, got %v", err)
	}
}

type fakeInfo struct{}

func (fakeInfo) Name() string       { return "nginx.conf" }
func (fakeInfo) Size() int64        { return 0 }
func (fakeInfo) Mode() os.FileMode  { return 0 }
func (fakeInfo) ModTime() time.Time { return time.Now() }
func (fakeInfo) IsDir() bool        { return false }
func (fakeInfo) Sys() any           { return nil }

func hasHint(hints []string, sub string) bool {
	for _, h := range hints {
		if strings.Contains(h, sub) {
			return true
		}
	}
	return false
}
