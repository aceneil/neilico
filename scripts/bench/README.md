# 反代性能对比基架（自研内置反代 vs nginx）

用途：给「内置反代到底够不够快」这个问题一个可复现的答案，并防止已修的性能回归复发。

## 复现步骤

```bash
# 1) 上游服务（固定小响应，避免上游成为瓶颈）
go run scripts/bench/proxy_bench_server.go            # 监听 :19090

# 2) 客户端
CGO_ENABLED=0 go build -o /tmp/benchclient scripts/bench/proxy_bench_client.go

# 3) NEILICO 规则：域名 → 该上游（示例用 internal_ip）
#    域名 app.example.test，规则 / → internal_ip 127.0.0.1:19090

# 4) nginx 对照（host 网络，同一上游）
docker run -d --name nginx-bench --network host \
  -v "$PWD/scripts/bench/nginx-bench.conf:/etc/nginx/nginx.conf:ro" nginx:alpine

# 5) 压测
/tmp/benchclient -url http://<入口>/ -host app.example.test -c 200 -d 15s     # 自研
/tmp/benchclient -url http://127.0.0.1:19091/ -c 200 -d 15s                    # nginx
/tmp/benchclient -url http://127.0.0.1:19090/ -c 200 -d 15s                    # 上游基线
```

## 基线（2026-10-05，8 核宿主机，同一上游、同一客户端）

| 路径 | 并发50 RPS | p50 | p99 | 并发200 RPS | p99 | 失败 |
| :--- | ---: | ---: | ---: | ---: | ---: | ---: |
| 上游直连（基线） | 28,974 | 1.33ms | 7.15ms | 29,102 | 4.92ms | 0 |
| 自研反代 **修复前** | 2,515 | 12.2ms | 119ms | 2,317 | **1,161ms** | **146** |
| 自研反代 **修复后** | 6,858 | 6.2ms | 24.9ms | 7,798 | 89.7ms | 0 |
| 自研反代 **修复后（容器内直连，无额外 NAT）** | — | — | — | **9,616** | 67.8ms | 0 |
| nginx（host 网络，同轮对照） | — | — | — | 8,822 | 52.5ms | 0 |

## 结论与不许回退的点

- 修复前比 nginx 慢约 4 倍、高并发下失败 146 —— 根因是**每请求新建 `httputil.ReverseProxy`**，
  使 `http.DefaultTransport` 的 `MaxIdleConnsPerHost(=2)` 暴露，几乎每请求重新建连
  （压测时容器 CPU 仅 8.8%，说明卡在等待而不是算力）。
- 修好后在同等网络条件下**吞吐已超 nginx（9,616 vs 8,822）**、中位延迟更低；
  p99 仍略逊（67.8ms vs 52.5ms），属于可接受残余。
- 对比必须注明网络路径：nginx 这里走 host 网络，而控制面容器是 bridge 网络，
  客户端跨容器时每请求多一跳 NAT（同一份代码 7,798 → 9,616 RPS 的差别就来自这里）。
- 回归测试见 `control-plane/internal/service/proxy/builtin_proxy_cache_test.go`
  （代理复用与失效、请求级转发头、连接池不得退回默认值）。
