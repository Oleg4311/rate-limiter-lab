package limiter

import (
	"math"
	"time"
)

// Скользящий счётчик: два фиксированных окна, предыдущее учитывается с весом оставшейся доли.
// Оценка = prev × (1 − прошло/window) + cur. Память O(1), ошибка небольшая, но не нулевая.
type slidingCounter struct {
	cfg Config
	s   *store[counterState]
}

type counterState struct {
	curStart int64
	cur      int
	prev     int
	last     int64
}

func newSlidingCounter(c Config) *slidingCounter {
	return &slidingCounter{cfg: c, s: newStore[counterState]()}
}

func (l *slidingCounter) Name() string { return SlidingCounter }

// at возвращает окна (prev, cur) и начало текущего окна для момента n, не меняя состояние.
func (st counterState) at(n, w int64) (prev, cur int, start int64) {
	start = floorDiv(n, w) * w
	switch {
	case st.curStart == start:
		return st.prev, st.cur, start
	case st.curStart+w == start:
		return st.cur, 0, start
	default:
		return 0, 0, start
	}
}

func estimate(prev, cur int, elapsed, w int64) float64 {
	return float64(prev)*(1-float64(elapsed)/float64(w)) + float64(cur)
}

// retryGuess решает неравенство оценки < limit относительно задержки приближённо (в float).
// Сначала пробует текущее окно, иначе следующее, где прошлым окном становится нынешнее.
func retryGuess(prev, cur int, elapsed, w int64, limit float64) int64 {
	if float64(cur) < limit && prev > 0 {
		x := float64(w) * (1 - (limit-float64(cur))/float64(prev)) // нужно elapsed + d > x
		d := int64(math.Ceil(x)) - elapsed
		if d < 0 {
			d = 0
		}
		if elapsed+d < w {
			return d
		}
	}
	rest := w - elapsed
	if float64(cur) < limit {
		return rest
	}
	return rest + int64(math.Ceil(float64(w)*(1-limit/float64(cur))))
}

// firstOK возвращает минимальную задержку, при которой ok истинно. Оценка монотонна, поэтому достаточно
// просмотреть несколько значений от догадки; если догадка оказалась далеко, работает двоичный поиск.
func firstOK(ok func(int64) bool, guess, hi int64) int64 {
	lo := guess - 3
	if lo < 0 {
		lo = 0
	}
	if lo == 0 || !ok(lo) { // lo не подходит, значит ответ правее
		for d := lo; d < lo+16; d++ {
			if ok(d) {
				return d
			}
		}
	}
	a, b := int64(0), hi
	for a < b {
		mid := a + (b-a)/2
		if ok(mid) {
			b = mid
		} else {
			a = mid + 1
		}
	}
	return a
}

func (l *slidingCounter) Allow(key string, now time.Time) Decision {
	w := int64(l.cfg.Window)
	limit := float64(l.cfg.Limit)
	return l.s.do(key, func(st *counterState) Decision {
		n := maxI64(nanos(now), st.last)
		st.last = n
		prev, cur, start := st.at(n, w)
		st.prev, st.cur, st.curStart = prev, cur, start
		if estimate(prev, cur, n-start, w) < limit {
			st.cur++
			rem := int(limit - estimate(st.prev, st.cur, n-start, w))
			if rem < 0 {
				rem = 0
			}
			return Decision{Allowed: true, Limit: l.cfg.Limit, Remaining: rem, ResetAfter: time.Duration(start + w - n)}
		}
		// Минимальная задержка d, после которой оценка опустится ниже лимита. Оценка не возрастает со временем
		// (без новых запросов). Формула даёт почти точный ответ, несколько шагов по той же функции оценки убирают
		// ошибку округления float. Отказы могут идти потоком, поэтому путь отказа обязан быть дешёвым.
		ok := func(d int64) bool {
			p, c, s := st.at(n+d, w)
			return estimate(p, c, n+d-s, w) < limit
		}
		d := firstOK(ok, retryGuess(prev, cur, n-start, w, limit), 2*w)
		return Decision{Limit: l.cfg.Limit, RetryAfter: time.Duration(d), ResetAfter: time.Duration(start + w - n)}
	})
}

func (l *slidingCounter) Sweep(now time.Time) int {
	w := int64(l.cfg.Window)
	n := nanos(now)
	return l.s.sweep(func(st *counterState) bool { return n >= st.curStart+2*w })
}

func (l *slidingCounter) Len() int { return l.s.len() }
