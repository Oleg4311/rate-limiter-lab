// Package service собирает демонстрационный HTTP-сервис: по одному ограниченному маршруту на каждый алгоритм.
// Нагрузочный тест (k6) бьёт в эти маршруты и сравнивает, сколько запросов пропустил каждый алгоритм.
package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Oleg4311/rate-limiter-lab/httpmw"
	"github.com/Oleg4311/rate-limiter-lab/limiter"
)

// Service — обработчик и доступ к ограничителям для фоновой очистки.
type Service struct {
	Handler  http.Handler
	Limiters map[string]limiter.Limiter
	Stats    map[string]*httpmw.Stats
}

// New создаёт сервис с одинаковой конфигурацией для всех алгоритмов. now можно подменить в тестах.
func New(cfg limiter.Config, now func() time.Time) (*Service, error) {
	if now == nil {
		now = time.Now
	}
	s := &Service{Limiters: map[string]limiter.Limiter{}, Stats: map[string]*httpmw.Stats{}}
	mux := http.NewServeMux()
	for _, name := range limiter.Algorithms {
		l, err := limiter.New(name, cfg)
		if err != nil {
			return nil, err
		}
		st := &httpmw.Stats{}
		s.Limiters[name], s.Stats[name] = l, st
		algo := name
		limited := httpmw.Middleware(l, httpmw.Options{Now: now, Stats: st})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(map[string]any{"algo": algo, "ok": true})
		}))
		mux.Handle("GET /limited/"+name, limited)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") })
	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, _ *http.Request) {
		out := map[string]map[string]int64{}
		for name, st := range s.Stats {
			out[name] = map[string]int64{"allowed": st.Allowed.Load(), "denied": st.Denied.Load()}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "rate-limiter-lab: GET /limited/{%v}, /stats, /healthz\n", limiter.Algorithms)
	})
	s.Handler = mux
	return s, nil
}

// SweepAll очищает простаивающие ключи всех ограничителей.
func (s *Service) SweepAll(now time.Time) int {
	n := 0
	for _, l := range s.Limiters {
		n += l.Sweep(now)
	}
	return n
}
