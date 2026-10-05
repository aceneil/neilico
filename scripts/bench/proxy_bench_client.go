// 反代压测客户端：固定并发 + 固定时长，输出 RPS / 平均 / p50 / p99 / 失败数。
//
//	go run scripts/bench/proxy_bench_client.go -url http://127.0.0.1:18081/ -host app.example.com -c 200 -d 15s
//
// 注意：要用容器内验证时先 CGO_ENABLED=0 go build（alpine 无 glibc 加载器），否则会报
// "exec ... no such file or directory"。
package main

import (
	"flag"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	url := flag.String("url", "", "target url")
	host := flag.String("host", "", "optional Host header override")
	conc := flag.Int("c", 50, "concurrency")
	dur := flag.Duration("d", 20*time.Second, "duration")
	flag.Parse()

	transport := &http.Transport{
		MaxIdleConnsPerHost: *conc,
		MaxIdleConns:        *conc * 2,
		DisableCompression:  true,
	}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}

	var ok, fail int64
	var mu sync.Mutex
	latencies := make([]time.Duration, 0, 1<<20)

	deadline := time.Now().Add(*dur)
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < *conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 4096)
			for time.Now().Before(deadline) {
				request, _ := http.NewRequest("GET", *url, nil)
				if *host != "" {
					request.Host = *host
				}
				t0 := time.Now()
				response, err := client.Do(request)
				if err != nil {
					atomic.AddInt64(&fail, 1)
					continue
				}
				n := 0
				for {
					read, readErr := response.Body.Read(buf)
					n += read
					if readErr != nil {
						break
					}
				}
				response.Body.Close()
				if response.StatusCode == 200 && n > 0 {
					atomic.AddInt64(&ok, 1)
					mu.Lock()
					if len(latencies) < cap(latencies) {
						latencies = append(latencies, time.Since(t0))
					}
					mu.Unlock()
				} else {
					atomic.AddInt64(&fail, 1)
				}
			}
		}()
	}
	wg.Wait()

	elapsed := time.Since(start)
	mu.Lock()
	sort.Slice(latencies, func(a, b int) bool { return latencies[a] < latencies[b] })
	var sum time.Duration
	for _, latency := range latencies {
		sum += latency
	}
	percentile := func(p float64) time.Duration {
		if len(latencies) == 0 {
			return 0
		}
		return latencies[int(float64(len(latencies)-1)*p)]
	}
	var average time.Duration
	if len(latencies) > 0 {
		average = sum / time.Duration(len(latencies))
	}
	mu.Unlock()

	fmt.Printf("  %-40s RPS=%8.0f  平均=%-10v p50=%-10v p99=%-10v 成功=%d 失败=%d\n",
		*url+"  host="+*host, float64(ok)/elapsed.Seconds(),
		average.Round(time.Microsecond), percentile(0.50).Round(time.Microsecond),
		percentile(0.99).Round(time.Microsecond), ok, fail)
}
