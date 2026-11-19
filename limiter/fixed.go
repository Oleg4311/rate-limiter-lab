package limiter

import "time"

// Фиксированное окно: счётчик сбрасывается на границах окон, выровненных по эпохе.
// Просто и дёшево (O(1) память), но на стыке окон пропускает до 2×limit запросов подряд.
type fixedWindow struct {
	cfg Config
	s   *store[fixedState]
}

type fixedState struct {
	start int64 // начало окна, нс
	count int
	last  int64
}

func newFixedWindow(c Config) *fixedWindow { return &fixedWindow{cfg: c, s: newStore[fixedState]()} }

func (l *fixedWindow) Name() string { return FixedWindow }

func (l *fixedWindow) Allow(key string, now time.Time) Decision {
	w := int64(l.cfg.Window)
	return l.s.do(key, func(st *fixedState) Decision {
		n := maxI64(nanos(now), st.last)
		st.last = n
		start := floorDiv(n, w) * w
		if st.start != start {
			st.start, st.count = start, 0
		}
		reset := time.Duration(start + w - n)
		if st.count < l.cfg.Limit {
			st.count++
			return Decision{Allowed: true, Limit: l.cfg.Limit, Remaining: l.cfg.Limit - st.count, ResetAfter: reset}
		}
		return Decision{Limit: l.cfg.Limit, RetryAfter: reset, ResetAfter: reset}
	})
}

func (l *fixedWindow) Sweep(now time.Time) int {
	w := int64(l.cfg.Window)
	n := nanos(now)
	return l.s.sweep(func(st *fixedState) bool { return n >= st.start+w })
}

func (l *fixedWindow) Len() int { return l.s.len() }
