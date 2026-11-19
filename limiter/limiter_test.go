package limiter

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var t0 = time.Unix(1_700_000_000, 0) // кратно секунде: границы фиксированных окон предсказуемы

func at(ms float64) time.Time { return t0.Add(time.Duration(ms * float64(time.Millisecond))) }

func must(t *testing.T, name string, c Config) Limiter {
	t.Helper()
	l, err := New(name, c)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func forEachAlgo(t *testing.T, fn func(t *testing.T, name string)) {
	for _, name := range Algorithms {
		t.Run(name, func(t *testing.T) { fn(t, name) })
	}
}

// maxInWindow — максимум принятых запросов в любом полуинтервале (t−w, t].
func maxInWindow(times []time.Time, w time.Duration) int {
	best, lo := 0, 0
	for hi := range times {
		for times[lo].Add(w).Compare(times[hi]) <= 0 {
			lo++
		}
		if hi-lo+1 > best {
			best = hi - lo + 1
		}
	}
	return best
}

func TestFixedWindowSequence(t *testing.T) {
	l := must(t, FixedWindow, Config{Limit: 3, Window: time.Second})
	for i, ms := range []float64{0, 100, 200} {
		d := l.Allow("k", at(ms))
		if !d.Allowed || d.Remaining != 2-i || d.Limit != 3 {
			t.Fatalf("запрос %d: %+v", i, d)
		}
	}
	d := l.Allow("k", at(300))
	if d.Allowed || d.RetryAfter != 700*time.Millisecond || d.Remaining != 0 {
		t.Fatalf("четвёртый запрос: %+v", d)
	}
	if !l.Allow("k", at(1000)).Allowed {
		t.Fatal("новое окно должно принять запрос")
	}
}

func TestSlidingLogMatchesReferenceModel(t *testing.T) {
	for seed := int64(1); seed <= 150; seed++ {
		rng := rand.New(rand.NewSource(seed))
		limit := 1 + rng.Intn(8)
		w := time.Duration(100+rng.Intn(900)) * time.Millisecond
		l := must(t, SlidingLog, Config{Limit: limit, Window: w})
		var allowed []time.Time
		now := t0
		for i := 0; i < 300; i++ {
			now = now.Add(time.Duration(rng.Int63n(int64(w) / int64(limit) * 2)))
			cnt := 0
			for _, a := range allowed {
				if a.After(now.Add(-w)) {
					cnt++
				}
			}
			want := cnt < limit
			if got := l.Allow("k", now).Allowed; got != want {
				t.Fatalf("seed %d шаг %d: получено %v, эталон %v (в окне %d из %d)", seed, i, got, want, cnt, limit)
			}
			if want {
				allowed = append(allowed, now)
			}
		}
	}
}

// Классический изъян фиксированного окна: пачка в конце окна и пачка в начале следующего проходят подряд.
func TestBoundaryBurstExposesFixedWindow(t *testing.T) {
	cfg := Config{Limit: 100, Window: time.Second}
	count := map[string]int{}
	for _, name := range Algorithms {
		l := must(t, name, cfg)
		for _, base := range []float64{900, 1100} { // 100 мс до границы окна и 100 мс после
			for i := 0; i < 100; i++ {
				if l.Allow("k", at(base+float64(i)*0.01)).Allowed {
					count[name]++
				}
			}
		}
	}
	if count[FixedWindow] != 200 {
		t.Errorf("fixed-window должен пропустить 2×limit = 200, получено %d", count[FixedWindow])
	}
	if count[SlidingLog] != 100 {
		t.Errorf("sliding-log никогда не превышает лимит: %d", count[SlidingLog])
	}
	if c := count[SlidingCounter]; c < 100 || c > 112 {
		t.Errorf("sliding-counter при пачке за 0,1 окна до границы пропускает около limit×(1+0,1): %d", c)
	}
	for _, name := range []string{TokenBucket, GCRA} {
		// пачка 100 расходует всю корзину, за 200 мс до второй пачки накапливается 20 токенов
		if c := count[name]; c < 119 || c > 121 {
			t.Errorf("%s: ожидалось около burst + rate×0,2 с = 120, получено %d", name, c)
		}
	}
	if count[TokenBucket] != count[GCRA] {
		t.Errorf("token-bucket и gcra обязаны совпадать: %d и %d", count[TokenBucket], count[GCRA])
	}
}

// Худший случай скользящего счётчика: он считает прошлое окно «равномерным», а пачка в его конце этому не соответствует.
// Тогда за одну секунду проходит почти 2×limit. Это свойство алгоритма, а не ошибка реализации.
func TestSlidingCounterWorstCase(t *testing.T) {
	l := must(t, SlidingCounter, Config{Limit: 100, Window: time.Second})
	n := 0
	for _, base := range []float64{990, 1900} { // пачка в конце окна, вторая через 0,91 с: обе внутри одной секунды
		for i := 0; i < 100; i++ {
			if l.Allow("k", at(base+float64(i)*0.01)).Allowed {
				n++
			}
		}
	}
	if n < 185 || n > 195 {
		t.Fatalf("ожидалось около 190 за 0,91 с, получено %d", n)
	}
	// скользящий журнал в том же сценарии держит лимит
	j := must(t, SlidingLog, Config{Limit: 100, Window: time.Second})
	m := 0
	for _, base := range []float64{990, 1900} {
		for i := 0; i < 100; i++ {
			if j.Allow("k", at(base+float64(i)*0.01)).Allowed {
				m++
			}
		}
	}
	if m != 100 {
		t.Fatalf("sliding-log: %d вместо 100", m)
	}
}

func TestMaxInAnyWindowOnRandomTraffic(t *testing.T) {
	for seed := int64(1); seed <= 60; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cfg := Config{Limit: 20, Window: time.Second}
		for _, name := range Algorithms {
			l := must(t, name, cfg)
			var allowed []time.Time
			now := t0
			for i := 0; i < 600; i++ {
				now = now.Add(time.Duration(rng.ExpFloat64() * float64(cfg.Window) / 50)) // в 2,5 раза быстрее лимита
				if l.Allow("k", now).Allowed {
					allowed = append(allowed, now)
				}
			}
			m := maxInWindow(allowed, cfg.Window)
			var bound int
			switch name {
			case SlidingLog:
				bound = cfg.Limit
			case FixedWindow:
				bound = 2 * cfg.Limit
			case SlidingCounter:
				bound = 2 * cfg.Limit // оценка приблизительна для неравномерного трафика, см. TestSlidingCounterWorstCase
			default: // корзина: пачка burst плюс то, что накопилось за окно
				bound = 2 * cfg.Limit
			}
			if m > bound {
				t.Errorf("seed %d %s: в окне пропущено %d, граница %d", seed, name, m, bound)
			}
		}
	}
}

func TestTokenBucketEqualsGCRA(t *testing.T) {
	for _, cfg := range []Config{{Limit: 100, Window: time.Second, Burst: 20}, {Limit: 10, Window: time.Second}, {Limit: 5, Window: 500 * time.Millisecond, Burst: 3}} {
		for seed := int64(1); seed <= 80; seed++ {
			rng := rand.New(rand.NewSource(seed))
			tb, g := must(t, TokenBucket, cfg), must(t, GCRA, cfg)
			unit := int64(cfg.Window) / int64(cfg.Limit)
			now := t0
			for i := 0; i < 400; i++ {
				now = now.Add(time.Duration(rng.Int63n(unit * 2))) // кратно шагу, чтобы не накапливать ошибку округления
				a, b := tb.Allow("k", now), g.Allow("k", now)
				if a.Allowed != b.Allowed {
					t.Fatalf("cfg %+v seed %d шаг %d: token-bucket %v, gcra %v", cfg, seed, i, a.Allowed, b.Allowed)
				}
				if d := a.RetryAfter - b.RetryAfter; d < -2 || d > 2 {
					t.Fatalf("RetryAfter различается: %v и %v", a.RetryAfter, b.RetryAfter)
				}
			}
		}
	}
}

// После отказа повтор ровно через RetryAfter проходит, а чуть раньше ещё нет.
func TestRetryAfterIsAccurate(t *testing.T) {
	forEachAlgo(t, func(t *testing.T, name string) {
		checked := 0
		for seed := int64(1); seed <= 200; seed++ {
			rng := rand.New(rand.NewSource(seed))
			cfg := Config{Limit: 1 + rng.Intn(10), Window: time.Duration(200+rng.Intn(800)) * time.Millisecond}
			l := must(t, name, cfg)
			now := t0.Add(time.Duration(rng.Int63n(int64(time.Second))))
			for i := 0; i < 100; i++ {
				now = now.Add(time.Duration(rng.Int63n(int64(cfg.Window)/int64(cfg.Limit)) / 4))
				d := l.Allow("k", now)
				if d.Allowed {
					continue
				}
				if d.RetryAfter <= 0 {
					t.Fatalf("seed %d: отказ без RetryAfter: %+v", seed, d)
				}
				early := d.RetryAfter - d.RetryAfter/100 - 1
				if l.Allow("k", now.Add(early)).Allowed {
					t.Fatalf("seed %d: запрос за %v до RetryAfter (%v) уже прошёл", seed, d.RetryAfter-early, d.RetryAfter)
				}
				if !l.Allow("k", now.Add(d.RetryAfter)).Allowed {
					t.Fatalf("seed %d: запрос через RetryAfter=%v отклонён", seed, d.RetryAfter)
				}
				checked++
				break
			}
		}
		if checked < 100 {
			t.Fatalf("проверено слишком мало отказов: %d", checked)
		}
	})
}

func TestConcurrentSameKeyNeverExceedsLimit(t *testing.T) {
	forEachAlgo(t, func(t *testing.T, name string) {
		l := must(t, name, Config{Limit: 100, Window: time.Second})
		var allowed atomic.Int64
		var wg sync.WaitGroup
		for g := 0; g < 64; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 200; i++ {
					if l.Allow("shared", at(0)).Allowed { // один и тот же момент: пропустить можно ровно limit
						allowed.Add(1)
					}
				}
			}()
		}
		wg.Wait()
		if allowed.Load() != 100 {
			t.Fatalf("пропущено %d вместо 100", allowed.Load())
		}
	})
}

func TestKeysAreIndependent(t *testing.T) {
	forEachAlgo(t, func(t *testing.T, name string) {
		l := must(t, name, Config{Limit: 5, Window: time.Second})
		var wg sync.WaitGroup
		var bad atomic.Int64
		for k := 0; k < 300; k++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				key := fmt.Sprintf("client-%d", k)
				n := 0
				for i := 0; i < 12; i++ {
					if l.Allow(key, at(0)).Allowed {
						n++
					}
				}
				if n != 5 {
					bad.Add(1)
				}
			}()
		}
		wg.Wait()
		if bad.Load() != 0 || l.Len() != 300 {
			t.Fatalf("ключей с неверным числом: %d, Len=%d", bad.Load(), l.Len())
		}
	})
}

func TestSweepRemovesOnlyIdleKeys(t *testing.T) {
	forEachAlgo(t, func(t *testing.T, name string) {
		l := must(t, name, Config{Limit: 5, Window: time.Second})
		for i := 0; i < 50; i++ {
			l.Allow(fmt.Sprintf("old-%d", i), at(0))
		}
		l.Allow("fresh", at(9990)) // к моменту очистки (9999 мс) этот ключ ещё активен для всех алгоритмов
		if n := l.Sweep(at(10)); n != 0 {
			t.Fatalf("сразу после запросов удалено %d активных ключей", n)
		}
		if n := l.Sweep(at(9_999)); n != 50 || l.Len() != 1 {
			t.Fatalf("удалено %d (ожидалось 50), осталось %d (ожидалось только fresh)", n, l.Len())
		}
		// удалённый ключ ведёт себя как новый
		for i := 0; i < 5; i++ {
			if !l.Allow("old-0", at(10_001)).Allowed {
				t.Fatal("после очистки ключ должен получить полную ёмкость")
			}
		}
	})
}

func TestClockGoingBackwardsIsClamped(t *testing.T) {
	forEachAlgo(t, func(t *testing.T, name string) {
		l := must(t, name, Config{Limit: 10, Window: time.Second})
		allowed := 0
		for i := 0; i < 50; i++ {
			if l.Allow("k", at(1000-float64(i)*5)).Allowed { // время убывает
				allowed++
			}
		}
		if allowed != 10 {
			t.Fatalf("пропущено %d вместо 10: откат часов не должен давать лишнего", allowed)
		}
	})
}

func TestDecisionFields(t *testing.T) {
	l := must(t, SlidingLog, Config{Limit: 3, Window: time.Second})
	for i, want := range []int{2, 1, 0} {
		d := l.Allow("k", at(float64(i)))
		if !d.Allowed || d.Remaining != want || d.Limit != 3 || d.RetryAfter != 0 {
			t.Fatalf("запрос %d: %+v", i, d)
		}
	}
	d := l.Allow("k", at(10))
	if d.Allowed || d.Remaining != 0 || d.RetryAfter != 990*time.Millisecond {
		t.Fatalf("отказ: %+v", d)
	}
}

func TestConfigValidationAndFactory(t *testing.T) {
	bad := []Config{{}, {Limit: 0, Window: time.Second}, {Limit: 1, Window: 0}, {Limit: 1, Window: time.Second, Burst: -1}, {Limit: 1_000_000_000_000, Window: time.Second}}
	for _, c := range bad {
		if _, err := New(GCRA, c); err == nil {
			t.Errorf("ожидалась ошибка для %+v", c)
		}
	}
	if _, err := New("leaky", Config{Limit: 1, Window: time.Second}); err == nil {
		t.Error("неизвестный алгоритм должен давать ошибку")
	}
	for _, name := range Algorithms {
		if l := must(t, name, Config{Limit: 1, Window: time.Second}); l.Name() != name {
			t.Errorf("Name() = %q, ожидалось %q", l.Name(), name)
		}
	}
}

func TestBurstDefaultsToLimit(t *testing.T) {
	for _, name := range []string{TokenBucket, GCRA} {
		l := must(t, name, Config{Limit: 10, Window: time.Second, Burst: 3})
		n := 0
		for i := 0; i < 10; i++ {
			if l.Allow("k", at(0)).Allowed {
				n++
			}
		}
		if n != 3 {
			t.Errorf("%s: burst=3 пропустил пачку из %d", name, n)
		}
		l = must(t, name, Config{Limit: 10, Window: time.Second})
		n = 0
		for i := 0; i < 20; i++ {
			if l.Allow("k", at(0)).Allowed {
				n++
			}
		}
		if n != 10 {
			t.Errorf("%s: burst по умолчанию равен limit, пропущено %d", name, n)
		}
	}
}

// Путь отказа должен быть не дороже пути пропуска: поток отклонённых запросов не должен нагружать сервер сильнее.
func BenchmarkDenied(b *testing.B) {
	for _, name := range Algorithms {
		l, _ := New(name, Config{Limit: 1, Window: time.Hour})
		l.Allow("k", t0)
		b.Run(name, func(b *testing.B) {
			now := t0
			for i := 0; i < b.N; i++ {
				now = now.Add(time.Microsecond)
				if l.Allow("k", now).Allowed {
					b.Fatal("ожидался отказ")
				}
			}
		})
	}
}

func BenchmarkAllow(b *testing.B) {
	for _, name := range Algorithms {
		l, _ := New(name, Config{Limit: 1000, Window: time.Second})
		b.Run(name+"/one-key", func(b *testing.B) {
			now := t0
			for i := 0; i < b.N; i++ {
				now = now.Add(time.Microsecond)
				l.Allow("k", now)
			}
		})
		b.Run(name+"/many-keys-parallel", func(b *testing.B) {
			keys := make([]string, 4096)
			for i := range keys {
				keys[i] = fmt.Sprintf("client-%d", i)
			}
			var ctr atomic.Int64
			b.RunParallel(func(pb *testing.PB) {
				i := int(ctr.Add(1)) * 7919
				now := t0
				for pb.Next() {
					i++
					now = now.Add(time.Microsecond)
					l.Allow(keys[i%len(keys)], now)
				}
			})
		})
	}
}
