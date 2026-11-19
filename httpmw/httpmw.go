// Package httpmw — HTTP-middleware поверх limiter: заголовки RateLimit-*, Retry-After и ответ 429.
package httpmw

import (
	"encoding/json"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/Oleg4311/rate-limiter-lab/limiter"
)

// KeyFunc выбирает ключ клиента по запросу.
type KeyFunc func(*http.Request) string

// DefaultKey использует заголовок X-API-Key, а без него IP-адрес соединения.
// Заголовок X-Forwarded-For намеренно не читается: любой клиент мог бы подделать его и обойти лимит.
// За доверенным прокси передайте свою KeyFunc.
func DefaultKey(r *http.Request) string {
	if k := r.Header.Get("X-API-Key"); k != "" {
		return "key:" + k
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}

// Stats — счётчики решений.
type Stats struct {
	Allowed atomic.Int64
	Denied  atomic.Int64
}

// Options настраивает middleware.
type Options struct {
	Key   KeyFunc
	Now   func() time.Time
	Stats *Stats
}

// seconds округляет вверх до целых секунд: Retry-After и RateLimit-Reset измеряются в целых секундах.
func seconds(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return int64(math.Ceil(d.Seconds()))
}

// Middleware ограничивает частоту запросов к обработчику.
func Middleware(l limiter.Limiter, o Options) func(http.Handler) http.Handler {
	if o.Key == nil {
		o.Key = DefaultKey
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			d := l.Allow(o.Key(r), o.Now())
			h := w.Header()
			h.Set("RateLimit-Limit", strconv.Itoa(d.Limit))
			h.Set("RateLimit-Remaining", strconv.Itoa(d.Remaining))
			h.Set("RateLimit-Reset", strconv.FormatInt(seconds(d.ResetAfter), 10))
			if d.Allowed {
				if o.Stats != nil {
					o.Stats.Allowed.Add(1)
				}
				next.ServeHTTP(w, r)
				return
			}
			if o.Stats != nil {
				o.Stats.Denied.Add(1)
			}
			retry := seconds(d.RetryAfter)
			if retry < 1 {
				retry = 1 // Retry-After 0 означал бы «повторяйте сразу»
			}
			h.Set("Retry-After", strconv.FormatInt(retry, 10))
			h.Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":          "too_many_requests",
				"message":        "Слишком много запросов",
				"retry_after_ms": d.RetryAfter.Milliseconds(),
			})
		})
	}
}
