package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Oleg4311/rate-limiter-lab/limiter"
)

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestRoutesAndUnknownAlgorithm(t *testing.T) {
	s, err := New(limiter.Config{Limit: 5, Window: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler)
	defer srv.Close()
	for _, name := range limiter.Algorithms {
		if code, body := get(t, srv.URL+"/limited/"+name); code != 200 || body == "" {
			t.Errorf("%s: %d %q", name, code, body)
		}
	}
	if code, _ := get(t, srv.URL+"/limited/nope"); code != 404 {
		t.Errorf("неизвестный алгоритм: %d", code)
	}
	if code, body := get(t, srv.URL+"/healthz"); code != 200 || body != "ok" {
		t.Errorf("healthz: %d %q", code, body)
	}
	var stats map[string]map[string]int64
	_, body := get(t, srv.URL+"/stats")
	if err := json.Unmarshal([]byte(body), &stats); err != nil || stats[limiter.GCRA]["allowed"] != 1 {
		t.Errorf("stats: %s (%v)", body, err)
	}
}

// Настоящие часы и настоящий HTTP: пачка быстрых запросов к корзине токенов.
// Допустимое число принятых зависит от того, сколько времени заняла пачка, поэтому границы вычисляются по измеренному времени.
func TestRealClockTokenBucketOverHTTP(t *testing.T) {
	const burst, rate = 5, 20 // 20 запросов/с, пачка 5
	s, _ := New(limiter.Config{Limit: rate, Window: time.Second, Burst: burst}, nil)
	srv := httptest.NewServer(s.Handler)
	defer srv.Close()

	start := time.Now()
	allowed := 0
	for i := 0; i < 80; i++ {
		if code, _ := get(t, srv.URL+"/limited/"+limiter.TokenBucket); code == 200 {
			allowed++
		}
	}
	elapsed := time.Since(start).Seconds()
	max := burst + int(float64(rate)*elapsed) + 2 // +2: запас на округление
	if allowed < burst || allowed > max {
		t.Fatalf("принято %d за %.3f с, ожидалось от %d до %d", allowed, elapsed, burst, max)
	}
}

func TestRealClockConcurrentClientsShareOneLimit(t *testing.T) {
	s, _ := New(limiter.Config{Limit: 50, Window: time.Minute}, nil) // окно заведомо длиннее теста
	srv := httptest.NewServer(s.Handler)
	defer srv.Close()
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 60; i++ {
				resp, err := http.Get(srv.URL + "/limited/" + limiter.SlidingLog)
				if err != nil {
					return
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode == 200 {
					allowed.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 50 {
		t.Fatalf("8 параллельных клиентов с одного IP пропущено %d вместо 50", allowed.Load())
	}
}

func TestSweepAll(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	s, _ := New(limiter.Config{Limit: 5, Window: time.Second}, func() time.Time { return now })
	srv := httptest.NewServer(s.Handler)
	defer srv.Close()
	get(t, srv.URL+"/limited/"+limiter.FixedWindow)
	if n := s.SweepAll(now.Add(time.Hour)); n < 1 {
		t.Fatalf("очищено %d", n)
	}
}
