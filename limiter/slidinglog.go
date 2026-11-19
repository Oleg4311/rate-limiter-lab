package limiter

import "time"

// Скользящий журнал: хранит момент каждого принятого запроса и считает точно, сколько их попало в (now−window, now].
// Самый точный алгоритм, но память O(limit) на ключ.
type slidingLog struct {
	cfg Config
	s   *store[logState]
}

type logState struct {
	ts   []int64 // моменты принятых запросов по возрастанию
	last int64
}

func newSlidingLog(c Config) *slidingLog { return &slidingLog{cfg: c, s: newStore[logState]()} }

func (l *slidingLog) Name() string { return SlidingLog }

func (st *logState) evict(cutoff int64) {
	i := 0
	for i < len(st.ts) && st.ts[i] <= cutoff {
		i++
	}
	if i == len(st.ts) {
		st.ts = st.ts[:0]
	} else if i > 0 {
		st.ts = append(st.ts[:0], st.ts[i:]...) // сдвиг без роста памяти
	}
}

func (l *slidingLog) Allow(key string, now time.Time) Decision {
	w := int64(l.cfg.Window)
	return l.s.do(key, func(st *logState) Decision {
		n := maxI64(nanos(now), st.last)
		st.last = n
		st.evict(n - w)
		if len(st.ts) < l.cfg.Limit {
			st.ts = append(st.ts, n)
			return Decision{
				Allowed:    true,
				Limit:      l.cfg.Limit,
				Remaining:  l.cfg.Limit - len(st.ts),
				ResetAfter: time.Duration(st.ts[len(st.ts)-1] + w - n),
			}
		}
		retry := time.Duration(st.ts[0] + w - n) // когда самый старый запрос выйдет из окна
		return Decision{Limit: l.cfg.Limit, RetryAfter: retry, ResetAfter: time.Duration(st.ts[len(st.ts)-1] + w - n)}
	})
}

func (l *slidingLog) Sweep(now time.Time) int {
	cutoff := nanos(now) - int64(l.cfg.Window)
	return l.s.sweep(func(st *logState) bool { return len(st.ts) == 0 || st.ts[len(st.ts)-1] <= cutoff })
}

func (l *slidingLog) Len() int { return l.s.len() }
