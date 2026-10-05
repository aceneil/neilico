package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// 反代性能回归：每请求新建 httputil.ReverseProxy 会让上游连接池退化成「几乎每次重新建连」，
// 实测吞吐掉 4 倍、p99 差一个数量级。以下三条守住这个修复。

func TestBuiltinReverseProxyIsReusedAndInvalidated(t *testing.T) {
	t.Parallel()
	handle := newProxyDB(t)
	builtin := NewBuiltin(handle, nil, nil, nil)

	route := Route{
		RuleID: uuid.New(), TenantID: uuid.New(),
		Host: "reuse.example.test", Path: "/", Target: "127.0.0.1:19090",
	}
	first, err := builtin.reverseProxyFor(route)
	if err != nil {
		t.Fatalf("首次构建代理失败: %v", err)
	}
	second, err := builtin.reverseProxyFor(route)
	if err != nil {
		t.Fatalf("二次取代理失败: %v", err)
	}
	if first != second {
		t.Fatal("同一规则必须复用同一个 ReverseProxy，否则上游连接池失效")
	}

	builtin.invalidateProxies()
	third, err := builtin.reverseProxyFor(route)
	if err != nil {
		t.Fatalf("重载后构建代理失败: %v", err)
	}
	if third == first {
		t.Fatal("路由重载后必须重建代理（目标/协议可能已变）")
	}
}

func TestBuiltinReverseProxyKeepsRequestScopedHeaders(t *testing.T) {
	t.Parallel()
	var gotHost, gotForwardedHost, gotForwardedProto string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotForwardedHost = r.Header.Get("X-Forwarded-Host")
		gotForwardedProto = r.Header.Get("X-Forwarded-Proto")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	handle := newProxyDB(t)
	builtin := NewBuiltin(handle, nil, nil, nil)
	route := Route{
		RuleID: uuid.New(), TenantID: uuid.New(),
		Host: "edge.example.test", Path: "/",
		Target: strings.TrimPrefix(upstream.URL, "http://"),
	}
	builtin.routes.Store(&RouteSet{
		byHost: map[string][]Route{"edge.example.test": {route}},
		Routes: []Route{route},
	})

	// 同一条规则、两种写法都命中同一路由（normalizeHost 统一大小写与端口），
	// 用它们验证复用的代理没有把上一次请求的 Host 串到下一次。
	for _, want := range []string{"edge.example.test", "EDGE.EXAMPLE.TEST:18081"} {
		request := httptest.NewRequest(http.MethodGet, "http://edge.example.test/", nil)
		request.Host = want
		recorder := httptest.NewRecorder()
		builtin.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("Host=%s 转发状态码 = %d，期望 204", want, recorder.Code)
		}
		if gotHost != want {
			t.Fatalf("上游收到 Host=%q，期望 %q", gotHost, want)
		}
		if gotForwardedHost != want {
			t.Fatalf("上游收到 X-Forwarded-Host=%q，期望 %q（复用了上一次请求的值？）", gotForwardedHost, want)
		}
		if gotForwardedProto != "http" {
			t.Fatalf("上游收到 X-Forwarded-Proto=%q，期望 http", gotForwardedProto)
		}
	}
}

func TestUpstreamTransportKeepsTunedConnectionPool(t *testing.T) {
	t.Parallel()
	transport := newUpstreamTransport()
	if transport.DisableKeepAlives {
		t.Fatal("上游 keep-alive 被关闭，连接无法复用")
	}
	if transport.MaxIdleConnsPerHost < 64 {
		t.Fatalf("每主机空闲连接上限被降到 %d（默认 2 会在并发下退化成每请求建连）", transport.MaxIdleConnsPerHost)
	}
	if transport.MaxIdleConns < 256 {
		t.Fatalf("总空闲连接上限被降到 %d", transport.MaxIdleConns)
	}
}
