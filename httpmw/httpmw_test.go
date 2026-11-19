package httpmw

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Oleg4311/rate-limiter-lab/limiter"
)

var epoch = time.Unix(1_700_000_000, 0)

func setup(t *testing.T, name string, c limiter.Config) (http.Handler, *atomic.Int64, *time.Time, *Stats) {
	t.Helper()
	l, err := limiter.New(name, c)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	now := epoch
	stats := &Stats{}
	h := Middleware(l, Options{Now: func() time.Time { return now }, Stats: stats})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.WriteString(w, "ok")
	}))
	return h, &calls, &now, stats
}

func do(h http.Handler, hdr map[string]string, remote string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = remote
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHeadersAndRejection(t *testing.T) {
	h, calls, now, stats := setup(t, limiter.SlidingLog, limiter.Config{Limit: 2, Window: 10 * time.Second})
	r1 := do(h, nil, "1.2.3.4:5000")
	if r1.Code != 200 || r1.Header().Get("RateLimit-Limit") != "2" || r1.Header().Get("RateLimit-Remaining") != "1" {
		t.Fatalf("первый запрос: %d %v", r1.Code, r1.Header())
	}
	*now = now.Add(2500 * time.Millisecond)
	r2 := do(h, nil, "1.2.3.4:5001") // другой порт, тот же IP
	if r2.Code != 200 || r2.Header().Get("RateLimit-Remaining") != "0" {
		t.Fatalf("второй запрос: %d %v", r2.Code, r2.Header())
	}
	r3 := do(h, nil, "1.2.3.4:5002")
	if r3.Code != http.StatusTooManyRequests {
		t.Fatalf("третий запрос: %d", r3.Code)
	}
	// самый старый запрос выйдет из окна через 10 − 2,5 = 7,5 с, округляется вверх
	if got := r3.Header().Get("Retry-After"); got != "8" {
		t.Errorf("Retry-After = %q, ожидалось 8", got)
	}
	if r3.Header().Get("RateLimit-Remaining") != "0" || r3.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Errorf("заголовки отказа: %v", r3.Header())
	}
	var body map[string]any
	if err := json.Unmarshal(r3.Body.Bytes(), &body); err != nil || body["error"] != "too_many_requests" || body["retry_after_ms"].(float64) != 7500 {
		t.Errorf("тело отказа: %s (%v)", r3.Body.String(), err)
	}
	if calls.Load() != 2 {
		t.Errorf("обработчик вызван %d раз, должен быть 2: отклонённые запросы до него не доходят", calls.Load())
	}
	if stats.Allowed.Load() != 2 || stats.Denied.Load() != 1 {
		t.Errorf("статистика: %d/%d", stats.Allowed.Load(), stats.Denied.Load())
	}
}

func TestRetryAfterNeverZero(t *testing.T) {
	h, _, _, _ := setup(t, limiter.TokenBucket, limiter.Config{Limit: 1000, Window: time.Second, Burst: 1})
	do(h, nil, "9.9.9.9:1")
	r := do(h, nil, "9.9.9.9:1") // до следующего токена 1 мс: в заголовке всё равно минимум 1 секунда
	if r.Code != 429 || r.Header().Get("Retry-After") != "1" {
		t.Fatalf("%d, Retry-After=%q", r.Code, r.Header().Get("Retry-After"))
	}
}

func TestKeysIPAndAPIKey(t *testing.T) {
	h, _, _, _ := setup(t, limiter.FixedWindow, limiter.Config{Limit: 1, Window: time.Minute})
	if do(h, nil, "10.0.0.1:1").Code != 200 || do(h, nil, "10.0.0.1:2").Code != 429 {
		t.Fatal("один IP делит лимит между соединениями")
	}
	if do(h, nil, "10.0.0.2:1").Code != 200 {
		t.Fatal("другой IP должен иметь свой лимит")
	}
	// ключ API важнее адреса
	if do(h, map[string]string{"X-API-Key": "alpha"}, "10.0.0.1:3").Code != 200 {
		t.Fatal("новый ключ API должен получить свой лимит")
	}
	if do(h, map[string]string{"X-API-Key": "alpha"}, "10.0.0.99:3").Code != 429 {
		t.Fatal("тот же ключ API с другого адреса делит лимит")
	}
}

func TestForwardedForIsNotTrusted(t *testing.T) {
	h, _, _, _ := setup(t, limiter.FixedWindow, limiter.Config{Limit: 1, Window: time.Minute})
	do(h, map[string]string{"X-Forwarded-For": "1.1.1.1"}, "10.0.0.1:1")
	// подмена X-Forwarded-For не должна давать новый лимит
	if do(h, map[string]string{"X-Forwarded-For": "2.2.2.2"}, "10.0.0.1:2").Code != 429 {
		t.Fatal("клиент обошёл лимит подменой X-Forwarded-For")
	}
}

func TestRecoversAfterWait(t *testing.T) {
	h, _, now, _ := setup(t, limiter.GCRA, limiter.Config{Limit: 1, Window: 2 * time.Second})
	do(h, nil, "5.5.5.5:1")
	if do(h, nil, "5.5.5.5:1").Code != 429 {
		t.Fatal("ожидался отказ")
	}
	*now = now.Add(2 * time.Second)
	if do(h, nil, "5.5.5.5:1").Code != 200 {
		t.Fatal("после ожидания запрос должен пройти")
	}
}
