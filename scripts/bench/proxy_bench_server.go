// 反代压测用的上游服务：返回固定小响应，避免上游成为瓶颈。
// 运行： go run scripts/bench/proxy_bench_server.go     （监听 :19090）
package main

import "net/http"

func main() {
	body := []byte("hello, neilico")
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		_, _ = w.Write(body)
	})
	_ = http.ListenAndServe("0.0.0.0:19090", nil)
}
