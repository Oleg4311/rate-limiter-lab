package limiter

import (
	"math"
	"time"
)

// Корзина токенов: ёмкость burst, пополнение limit/window токенов в единицу времени, запрос стоит один токен.
// Разрешает пачку до burst, затем держит среднюю скорость. Память O(1).
type tokenBucket struct {
	cfg   Config
	burst float64
	rate  float64 // токенов в наносекунду
	s     *store[bucketState]
}

type bucketState struct {
	tokens float64
	last   int64
	init   bool
}

// eps защищает от ошибки округления float: 0.9999999999 токена считается целым.
const eps = 1e-9

func newTokenBucket(c Config) *tokenBucket {
	return &tokenBucket{cfg: c, burst: float64(c.burst()), rate: float64(c.Limit) / float64(c.Window), s: newStore[bucketState]()}
}

func (l *tokenBucket) Name() string { return TokenBucket }

func (l *tokenBucket) refill(st *bucketState, n int64) {
	if !st.init {
		st.tokens, st.last, st.init = l.burst, n, true
		return
	}
	n = maxI64(n, st.last)
	st.tokens = math.Min(l.burst, st.tokens+float64(n-st.last)*l.rate)
	st.last = n
}

func (l *tokenBucket) Allow(key string, now time.Time) Decision {
	return l.s.do(key, func(st *bucketState) Decision {
		l.refill(st, nanos(now))
		toFull := time.Duration(math.Ceil((l.burst - st.tokens) / l.rate))
		if st.tokens >= 1-eps {
			st.tokens = math.Max(0, st.tokens-1)
			return Decision{Allowed: true, Limit: l.cfg.Limit, Remaining: int(math.Floor(st.tokens + eps)), ResetAfter: time.Duration(math.Ceil((l.burst - st.tokens) / l.rate))}
		}
		retry := time.Duration(math.Ceil((1 - eps - st.tokens) / l.rate))
		return Decision{Limit: l.cfg.Limit, RetryAfter: retry, ResetAfter: toFull}
	})
}

func (l *tokenBucket) Sweep(now time.Time) int {
	n := nanos(now)
	return l.s.sweep(func(st *bucketState) bool {
		return !st.init || st.tokens+float64(maxI64(n-st.last, 0))*l.rate >= l.burst
	})
}

func (l *tokenBucket) Len() int { return l.s.len() }
