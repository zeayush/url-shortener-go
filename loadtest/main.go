// loadtest hammers the redirect endpoint and reports throughput & latency.
// Usage: go run ./loadtest -url http://localhost:8080/NpQOKW -c 200 -n 500000
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

func main() {
	target := flag.String("url", "http://localhost:8080/NpQOKW", "URL to benchmark")
	concurrency := flag.Int("c", 200, "concurrent workers")
	total := flag.Int("n", 500_000, "total requests")
	flag.Parse()

	transport := &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        *concurrency + 100,
		MaxIdleConnsPerHost: *concurrency + 100,
		IdleConnTimeout:     90 * time.Second,
	}
	client := &http.Client{
		Transport: transport,
		// Do NOT follow redirects — we measure server response, not the target.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	var (
		success  atomic.Int64
		failures atomic.Int64
		latsMu   sync.Mutex
		lats     = make([]float64, 0, *total)
	)

	work := make(chan struct{}, *total)
	for i := 0; i < *total; i++ {
		work <- struct{}{}
	}
	close(work)

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(*concurrency)

	for i := 0; i < *concurrency; i++ {
		go func() {
			defer wg.Done()
			for range work {
				t0 := time.Now()
				resp, err := client.Get(*target)
				elapsed := time.Since(t0).Seconds() * 1000 // ms
				if err != nil {
					failures.Add(1)
					continue
				}
				resp.Body.Close()
				if resp.StatusCode == 302 || resp.StatusCode == 200 {
					success.Add(1)
				} else {
					failures.Add(1)
				}
				latsMu.Lock()
				lats = append(lats, elapsed)
				latsMu.Unlock()
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	sort.Float64s(lats)
	p50 := percentile(lats, 50)
	p95 := percentile(lats, 95)
	p99 := percentile(lats, 99)
	avg := avg(lats)

	rps := float64(success.Load()) / elapsed.Seconds()

	fmt.Fprintf(os.Stdout, "\n=== Load Test Results ===\n")
	fmt.Fprintf(os.Stdout, "URL          : %s\n", *target)
	fmt.Fprintf(os.Stdout, "Concurrency  : %d workers\n", *concurrency)
	fmt.Fprintf(os.Stdout, "Total time   : %.2fs\n", elapsed.Seconds())
	fmt.Fprintf(os.Stdout, "Requests     : %d sent\n", *total)
	fmt.Fprintf(os.Stdout, "Success      : %d (%.1f%%)\n", success.Load(), float64(success.Load())/float64(*total)*100)
	fmt.Fprintf(os.Stdout, "Failures     : %d\n", failures.Load())
	fmt.Fprintf(os.Stdout, "Throughput   : %.0f req/sec\n", rps)
	fmt.Fprintf(os.Stdout, "Latency avg  : %.2f ms\n", avg)
	fmt.Fprintf(os.Stdout, "Latency p50  : %.2f ms\n", p50)
	fmt.Fprintf(os.Stdout, "Latency p95  : %.2f ms\n", p95)
	fmt.Fprintf(os.Stdout, "Latency p99  : %.2f ms\n", p99)
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p / 100)
	return sorted[idx]
}

func avg(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	return sum / float64(len(vals))
}
