package lab

import (
	"encoding/xml"
	"flag"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Oleg4311/rate-limiter-lab/limiter"
)

func base(p Pattern) Scenario {
	return Scenario{Pattern: p, Limit: 100, WindowMs: 1000, Rate: 200, DurationMs: 6000, Seed: 7}
}

func byName(t *testing.T, r Result, name string) AlgoResult {
	t.Helper()
	for _, a := range r.Algorithm {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("нет результата для %s", name)
	return AlgoResult{}
}

func TestBoundaryPatternExposesFixedWindow(t *testing.T) {
	r, err := Run(base(Boundary))
	if err != nil {
		t.Fatal(err)
	}
	if got := byName(t, r, limiter.FixedWindow).MaxInWindow; got != 200 {
		t.Errorf("fixed-window: %d в окне вместо 200", got)
	}
	if got := byName(t, r, limiter.SlidingLog).MaxInWindow; got != 100 {
		t.Errorf("sliding-log: %d в окне вместо 100", got)
	}
	if got := byName(t, r, limiter.SlidingCounter).MaxInWindow; got < 100 || got > 115 {
		t.Errorf("sliding-counter: %d", got)
	}
	tb, g := byName(t, r, limiter.TokenBucket), byName(t, r, limiter.GCRA)
	if tb.MaxInWindow < 115 || tb.MaxInWindow > 125 || tb.Allowed != g.Allowed {
		t.Errorf("token-bucket %d/%d, gcra %d", tb.MaxInWindow, tb.Allowed, g.Allowed)
	}
}

func TestBelowLimitNothingIsDenied(t *testing.T) {
	s := base(Steady)
	s.Rate = 50 // вдвое меньше лимита
	r, err := Run(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range r.Algorithm {
		if a.Denied != 0 || a.FirstDenyMs != -1 {
			t.Errorf("%s отклонил %d запросов при нагрузке вдвое ниже лимита", a.Name, a.Denied)
		}
	}
}

func TestOverloadIsLimitedToRoughlyTheRate(t *testing.T) {
	r, err := Run(base(Steady)) // 200 запросов/с при лимите 100/с, 6 секунд
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range r.Algorithm {
		if a.Allowed < 550 || a.Allowed > 750 {
			t.Errorf("%s пропустил %d за 6 с (ожидалось около 600-700)", a.Name, a.Allowed)
		}
		if a.Allowed+a.Denied != r.Offered {
			t.Errorf("%s: %d+%d != %d", a.Name, a.Allowed, a.Denied, r.Offered)
		}
		sum := 0
		for i := range a.AllowedByBucket {
			sum += a.AllowedByBucket[i] + a.DeniedByBucket[i]
		}
		if sum != r.Offered {
			t.Errorf("%s: в столбцах %d запросов вместо %d", a.Name, sum, r.Offered)
		}
	}
}

func TestRunIsDeterministic(t *testing.T) {
	a, _ := Run(base(Poisson))
	b, _ := Run(base(Poisson))
	if !reflect.DeepEqual(a, b) {
		t.Fatal("одинаковый сценарий дал разные результаты")
	}
	s := base(Poisson)
	s.Seed = 8
	c, _ := Run(s)
	if reflect.DeepEqual(a, c) {
		t.Fatal("другой seed должен менять поток")
	}
}

func TestArrivalsAreSortedAndInRange(t *testing.T) {
	for _, p := range Patterns {
		s := base(p.ID)
		s.Normalize()
		arr := Arrivals(s)
		if len(arr) == 0 {
			t.Errorf("%s: пустой поток", p.ID)
		}
		if !sort.SliceIsSorted(arr, func(i, j int) bool { return arr[i] < arr[j] }) {
			t.Errorf("%s: поток не отсортирован", p.ID)
		}
		for _, a := range arr {
			if a < 0 || a >= s.Duration+s.Window {
				t.Fatalf("%s: момент %v вне диапазона", p.ID, a)
			}
		}
	}
}

func TestRampVolumeMatchesTheory(t *testing.T) {
	s := base(Ramp)
	s.Normalize()
	got := len(Arrivals(s))
	want := int(s.Rate * s.Duration.Seconds()) // интеграл линейной интенсивности от 0 до 2·Rate
	if got < want-want/100 || got > want+want/100 {
		t.Fatalf("запросов %d, ожидалось около %d", got, want)
	}
}

func TestValidate(t *testing.T) {
	cases := map[string]func(*Scenario){
		"паттерн":           func(s *Scenario) { s.Pattern = "x" },
		"limit":             func(s *Scenario) { s.Limit = 0 },
		"окно слишком мало": func(s *Scenario) { s.WindowMs = 5 },
		"длительность":      func(s *Scenario) { s.DurationMs = 500 },
		"частота":           func(s *Scenario) { s.Rate = 0 },
		"слишком много":     func(s *Scenario) { s.Rate = 100_000; s.DurationMs = 600_000 },
	}
	for name, mut := range cases {
		s := base(Steady)
		mut(&s)
		if _, err := Run(s); err == nil {
			t.Errorf("%s: ожидалась ошибка", name)
		}
	}
}

func TestMaxInWindow(t *testing.T) {
	ms := func(v ...int) []time.Duration {
		out := make([]time.Duration, len(v))
		for i, x := range v {
			out[i] = time.Duration(x) * time.Millisecond
		}
		return out
	}
	// окно (t−1с, t]: для t=1000 это 10, 20, 1000 (ноль уже вышел), для t=1010 это 20, 1000, 1010, для t=20 это 0, 10, 20
	if got := maxInWindow(ms(0, 10, 20, 1000, 1010), time.Second); got != 3 {
		t.Fatalf("получено %d, ожидалось 3", got)
	}
	if got := maxInWindow(ms(0, 1000, 2000), time.Second); got != 1 {
		t.Fatalf("точки ровно на расстоянии окна не должны попадать в одно окно: %d", got)
	}
	if got := maxInWindow(nil, time.Second); got != 0 {
		t.Fatalf("пустой список: %d", got)
	}
}

var update = flag.Bool("update", false, "перезаписать docs/*.svg")

// Графики в docs/ лежат в README: тест гарантирует, что они соответствуют коду. Обновить: go test ./lab -update
func TestDocsAreUpToDate(t *testing.T) {
	docs := map[string]Scenario{
		"boundary.svg": {Pattern: Boundary, Limit: 100, Window: time.Second, Duration: 6 * time.Second, Rate: 200, Seed: 1},
		"burst.svg":    {Pattern: Burst, Limit: 100, Window: time.Second, Duration: 8 * time.Second, Rate: 200, Seed: 1},
		"poisson.svg":  {Pattern: Poisson, Limit: 100, Window: time.Second, Duration: 10 * time.Second, Rate: 150, Seed: 7},
	}
	for file, s := range docs {
		r, err := Run(s)
		if err != nil {
			t.Fatal(err)
		}
		want := SVG(r)
		path := filepath.Join("..", "docs", file)
		if *update {
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (создайте: go test ./lab -update)", file, err)
		}
		if string(got) != want {
			t.Errorf("%s устарел, выполните: go test ./lab -update", file)
		}
	}
}

func TestSVGIsWellFormedAndDeterministic(t *testing.T) {
	r, err := Run(base(Boundary))
	if err != nil {
		t.Fatal(err)
	}
	svg := SVG(r)
	dec := xml.NewDecoder(strings.NewReader(svg))
	for {
		if _, err := dec.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("SVG не является корректным XML: %v", err)
		}
	}
	for _, name := range limiter.Algorithms {
		if !strings.Contains(svg, name) {
			t.Errorf("в SVG нет алгоритма %s", name)
		}
	}
	if !strings.Contains(svg, "выше лимита в 2,00 раза") {
		t.Error("график должен показывать двукратное превышение у fixed-window")
	}
	if svg != SVG(r) {
		t.Error("SVG недетерминирован")
	}
}
