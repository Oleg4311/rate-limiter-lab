package limiter

import "time"

// GCRA (generic cell rate algorithm): хранит одно число, «теоретический момент прихода» TAT.
// Каждый принятый запрос сдвигает TAT на интервал T = window/limit; запрос проходит, если TAT не убежал
// от текущего момента больше, чем на допуск burst×T. Математически эквивалентен корзине токенов (и «дырявому ведру»
// как счётчику), но состояние — одно целое число без плавающей точки.
type gcra struct {
	cfg Config
	t   int64 // интервал между запросами, нс
	tau int64 // допуск: burst × T
	s   *store[gcraState]
}

type gcraState struct {
	tat  int64
	last int64
}

func newGCRA(c Config) *gcra {
	t := int64(c.Window) / int64(c.Limit)
	return &gcra{cfg: c, t: t, tau: int64(c.burst()) * t, s: newStore[gcraState]()}
}

func (l *gcra) Name() string { return GCRA }

func (l *gcra) Allow(key string, now time.Time) Decision {
	return l.s.do(key, func(st *gcraState) Decision {
		n := maxI64(nanos(now), st.last)
		st.last = n
		newTAT := maxI64(st.tat, n) + l.t
		allowAt := newTAT - l.tau
		if n >= allowAt {
			st.tat = newTAT
			rem := int((l.tau - (newTAT - n)) / l.t)
			if rem < 0 {
				rem = 0
			}
			return Decision{Allowed: true, Limit: l.cfg.Limit, Remaining: rem, ResetAfter: time.Duration(newTAT - n)}
		}
		return Decision{Limit: l.cfg.Limit, RetryAfter: time.Duration(allowAt - n), ResetAfter: time.Duration(maxI64(st.tat-n, 0))}
	})
}

func (l *gcra) Sweep(now time.Time) int {
	n := nanos(now)
	return l.s.sweep(func(st *gcraState) bool { return st.tat <= n })
}

func (l *gcra) Len() int { return l.s.len() }
