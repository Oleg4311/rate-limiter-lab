// Package lab прогоняет все алгоритмы на одном и том же детерминированном трафике и сравнивает результат.
// Время виртуальное: симуляция на тысячи запросов занимает миллисекунды и воспроизводится бит в бит.
package lab

import (
	"errors"
	"math"
	"math/rand"
	"sort"
	"time"

	"github.com/Oleg4311/rate-limiter-lab/limiter"
)

// Pattern — форма входящего трафика.
type Pattern string

const (
	Steady   Pattern = "steady"
	Burst    Pattern = "burst"
	Boundary Pattern = "boundary"
	Poisson  Pattern = "poisson"
	Ramp     Pattern = "ramp"
)

// PatternInfo — описание паттерна для интерфейса.
type PatternInfo struct {
	ID    Pattern `json:"id"`
	Title string  `json:"title"`
	About string  `json:"about"`
}

// Patterns — доступные паттерны.
var Patterns = []PatternInfo{
	{Steady, "Ровный поток", "Запросы идут через равные промежутки с заданной частотой."},
	{Burst, "Пачки", "Тишина, затем пачка из 3×limit запросов за 50 мс. Видно, как алгоритмы реагируют на всплеск."},
	{Boundary, "Пачки на границе окна", "Limit запросов в конце окна и ещё limit в начале следующего. Показывает главный изъян фиксированного окна."},
	{Poisson, "Случайный поток", "Пуассоновский поток с заданной средней частотой и фиксированным seed."},
	{Ramp, "Нарастающая нагрузка", "Частота растёт линейно от нуля до удвоенной заданной."},
}

// Scenario — параметры эксперимента.
type Scenario struct {
	Pattern  Pattern       `json:"pattern"`
	Limit    int           `json:"limit"`
	Window   time.Duration `json:"-"`
	WindowMs int           `json:"windowMs"`
	Burst    int           `json:"burst"`
	// Rate — предлагаемая частота, запросов в секунду (для steady, poisson, ramp).
	Rate       float64       `json:"rate"`
	Duration   time.Duration `json:"-"`
	DurationMs int           `json:"durationMs"`
	Seed       int64         `json:"seed"`
}

// Normalize заполняет Window и Duration из миллисекундных полей.
func (s *Scenario) Normalize() {
	if s.WindowMs > 0 {
		s.Window = time.Duration(s.WindowMs) * time.Millisecond
	}
	if s.DurationMs > 0 {
		s.Duration = time.Duration(s.DurationMs) * time.Millisecond
	}
	s.WindowMs = int(s.Window / time.Millisecond)
	s.DurationMs = int(s.Duration / time.Millisecond)
}

// Validate проверяет сценарий.
func (s Scenario) Validate() error {
	known := false
	for _, p := range Patterns {
		known = known || p.ID == s.Pattern
	}
	switch {
	case !known:
		return errors.New("неизвестный паттерн трафика")
	case s.Limit < 1 || s.Limit > 10_000:
		return errors.New("limit должен быть от 1 до 10000")
	case s.Window < 10*time.Millisecond || s.Window > time.Minute:
		return errors.New("окно должно быть от 10 мс до 60 с")
	case s.Duration < s.Window || s.Duration > 10*time.Minute:
		return errors.New("длительность должна быть не меньше окна и не больше 10 минут")
	case s.Burst < 0:
		return errors.New("burst не может быть отрицательным")
	case (s.Pattern == Steady || s.Pattern == Poisson || s.Pattern == Ramp) && (s.Rate <= 0 || s.Rate > 100_000):
		return errors.New("частота должна быть от 0 до 100000 запросов в секунду")
	}
	if n := expectedCount(s); n > 200_000 {
		return errors.New("слишком много запросов для симуляции (больше 200000): уменьшите частоту или длительность")
	}
	return nil
}

func expectedCount(s Scenario) int {
	switch s.Pattern {
	case Steady, Poisson:
		return int(s.Rate * s.Duration.Seconds())
	case Ramp:
		return int(s.Rate * s.Duration.Seconds())
	}
	return int(float64(s.Duration/s.Window)+1) * 3 * s.Limit
}

// Arrivals возвращает моменты запросов (смещения от начала) по возрастанию.
func Arrivals(s Scenario) []time.Duration {
	var out []time.Duration
	d := s.Duration
	switch s.Pattern {
	case Steady:
		gap := time.Duration(float64(time.Second) / s.Rate)
		if gap < 1 {
			gap = 1
		}
		for t := time.Duration(0); t < d; t += gap {
			out = append(out, t)
		}
	case Poisson:
		rng := rand.New(rand.NewSource(s.Seed))
		t := time.Duration(0)
		for {
			t += time.Duration(rng.ExpFloat64() / s.Rate * float64(time.Second))
			if t >= d {
				break
			}
			out = append(out, t)
		}
	case Ramp:
		// интенсивность r(t) = 2·Rate·t/D, накопленное число N(t) = Rate·t²/D, значит i-й запрос в t = sqrt(i·D/Rate)
		for i := 1; ; i++ {
			t := time.Duration(math.Sqrt(float64(i)*d.Seconds()/s.Rate) * float64(time.Second))
			if t >= d {
				break
			}
			out = append(out, t)
		}
	case Burst:
		// каждые 2 окна: окно тишины, затем 3×limit запросов за 50 мс
		for start := s.Window; start < d; start += 2 * s.Window {
			n := 3 * s.Limit
			for i := 0; i < n; i++ {
				out = append(out, start+time.Duration(float64(i)/float64(n)*float64(50*time.Millisecond)))
			}
		}
	case Boundary:
		// на каждой нечётной границе окна: limit запросов за 100 мс до неё и limit за 100 мс после
		span := 100 * time.Millisecond
		if span > s.Window/4 {
			span = s.Window / 4
		}
		for k := time.Duration(1); k*s.Window < d; k += 2 {
			edge := k * s.Window
			for i := 0; i < s.Limit; i++ {
				out = append(out, edge-span+time.Duration(float64(i)/float64(s.Limit)*float64(span-time.Millisecond)))
			}
			for i := 0; i < s.Limit; i++ {
				out = append(out, edge+time.Millisecond+time.Duration(float64(i)/float64(s.Limit)*float64(span-time.Millisecond)))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// AlgoResult — итог одного алгоритма.
type AlgoResult struct {
	Name    string `json:"name"`
	Allowed int    `json:"allowed"`
	Denied  int    `json:"denied"`
	// MaxInWindow — наибольшее число принятых запросов в любом окне длиной Window.
	MaxInWindow int `json:"maxInWindow"`
	// FirstDenyMs — момент первого отказа в миллисекундах, -1 если отказов не было.
	FirstDenyMs     int   `json:"firstDenyMs"`
	AllowedByBucket []int `json:"allowedByBucket"`
	DeniedByBucket  []int `json:"deniedByBucket"`
}

// Result — итог эксперимента.
type Result struct {
	Scenario  Scenario     `json:"scenario"`
	Offered   int          `json:"offered"`
	BucketMs  float64      `json:"bucketMs"`
	Buckets   int          `json:"buckets"`
	Algorithm []AlgoResult `json:"algorithms"`
}

// Buckets — число столбцов на графике.
const Buckets = 60

var epoch = time.Unix(1_700_000_000, 0) // кратно секунде

// Run прогоняет все алгоритмы на сценарии.
func Run(s Scenario) (Result, error) {
	s.Normalize()
	if err := s.Validate(); err != nil {
		return Result{}, err
	}
	arr := Arrivals(s)
	cfg := limiter.Config{Limit: s.Limit, Window: s.Window, Burst: s.Burst}
	bucket := s.Duration / Buckets
	if bucket < 1 {
		bucket = 1
	}
	res := Result{Scenario: s, Offered: len(arr), BucketMs: float64(bucket) / float64(time.Millisecond), Buckets: Buckets}
	for _, name := range limiter.Algorithms {
		l, err := limiter.New(name, cfg)
		if err != nil {
			return Result{}, err
		}
		ar := AlgoResult{Name: name, FirstDenyMs: -1, AllowedByBucket: make([]int, Buckets), DeniedByBucket: make([]int, Buckets)}
		var allowedAt []time.Duration
		for _, off := range arr {
			b := int(off / bucket)
			if b >= Buckets {
				b = Buckets - 1
			}
			if l.Allow("client", epoch.Add(off)).Allowed {
				ar.Allowed++
				ar.AllowedByBucket[b]++
				allowedAt = append(allowedAt, off)
			} else {
				ar.Denied++
				ar.DeniedByBucket[b]++
				if ar.FirstDenyMs < 0 {
					ar.FirstDenyMs = int(off / time.Millisecond)
				}
			}
		}
		ar.MaxInWindow = maxInWindow(allowedAt, s.Window)
		res.Algorithm = append(res.Algorithm, ar)
	}
	return res, nil
}

// maxInWindow — наибольшее число точек в полуинтервале (t−w, t].
func maxInWindow(ts []time.Duration, w time.Duration) int {
	best, lo := 0, 0
	for hi := range ts {
		for ts[lo]+w <= ts[hi] {
			lo++
		}
		if n := hi - lo + 1; n > best {
			best = n
		}
	}
	return best
}
