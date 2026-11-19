// Package limiter реализует пять алгоритмов ограничения частоты запросов за общим интерфейсом.
//
// Время передаётся в каждый вызов (now), поэтому алгоритмы детерминированы и проверяются без sleep.
// Время для одного ключа должно не убывать: если часы «пошли назад», момент округляется до последнего увиденного.
package limiter

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Config — параметры ограничения: не более Limit запросов за Window.
type Config struct {
	Limit  int
	Window time.Duration
	// Burst — размер «пачки» для token-bucket и gcra. Ноль означает Limit.
	Burst int
}

func (c Config) burst() int {
	if c.Burst > 0 {
		return c.Burst
	}
	return c.Limit
}

// Validate проверяет параметры.
func (c Config) Validate() error {
	switch {
	case c.Limit <= 0:
		return errors.New("limit должен быть положительным")
	case c.Window <= 0:
		return errors.New("window должен быть положительным")
	case c.Burst < 0:
		return errors.New("burst не может быть отрицательным")
	case int64(c.Window)/int64(c.Limit) < 1:
		return errors.New("слишком высокая частота: интервал между запросами меньше наносекунды")
	}
	return nil
}

// Decision — решение по одному запросу.
type Decision struct {
	Allowed bool
	// Limit — лимит за окно (для заголовка RateLimit-Limit).
	Limit int
	// Remaining — сколько запросов ещё пройдёт прямо сейчас.
	Remaining int
	// RetryAfter — через сколько повторить, если запрос отклонён (ноль при Allowed).
	RetryAfter time.Duration
	// ResetAfter — ориентир для RateLimit-Reset: через сколько восстановится полная ёмкость (или откроется окно).
	ResetAfter time.Duration
}

// Limiter — ограничитель с отдельным состоянием на каждый ключ (клиента).
type Limiter interface {
	Name() string
	// Allow решает, пропустить ли запрос ключа в момент now.
	Allow(key string, now time.Time) Decision
	// Sweep удаляет состояние простаивающих ключей и возвращает их число.
	Sweep(now time.Time) int
	// Len возвращает число ключей с состоянием.
	Len() int
}

// Названия алгоритмов.
const (
	FixedWindow    = "fixed-window"
	SlidingLog     = "sliding-log"
	SlidingCounter = "sliding-counter"
	TokenBucket    = "token-bucket"
	GCRA           = "gcra"
)

// Algorithms — все алгоритмы в порядке показа.
var Algorithms = []string{FixedWindow, SlidingLog, SlidingCounter, TokenBucket, GCRA}

// New создаёт ограничитель по имени алгоритма.
func New(name string, c Config) (Limiter, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	switch name {
	case FixedWindow:
		return newFixedWindow(c), nil
	case SlidingLog:
		return newSlidingLog(c), nil
	case SlidingCounter:
		return newSlidingCounter(c), nil
	case TokenBucket:
		return newTokenBucket(c), nil
	case GCRA:
		return newGCRA(c), nil
	}
	return nil, fmt.Errorf("неизвестный алгоритм %q, доступны: %v", name, Algorithms)
}

// ---- шардированное хранилище состояний ----

const shardCount = 64

type shard[S any] struct {
	mu sync.Mutex
	m  map[string]*S
}

type store[S any] struct{ shards [shardCount]shard[S] }

func newStore[S any]() *store[S] {
	s := &store[S]{}
	for i := range s.shards {
		s.shards[i].m = map[string]*S{}
	}
	return s
}

func (s *store[S]) shardOf(key string) *shard[S] {
	h := uint32(2166136261) // FNV-1a
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return &s.shards[h%shardCount]
}

// do выполняет fn над состоянием ключа под блокировкой его шарда; новое состояние создаётся нулевым.
func (s *store[S]) do(key string, fn func(st *S) Decision) Decision {
	sh := s.shardOf(key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	st := sh.m[key]
	if st == nil {
		st = new(S)
		sh.m[key] = st
	}
	return fn(st)
}

func (s *store[S]) sweep(idle func(*S) bool) int {
	removed := 0
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		for k, st := range sh.m {
			if idle(st) {
				delete(sh.m, k)
				removed++
			}
		}
		sh.mu.Unlock()
	}
	return removed
}

func (s *store[S]) len() int {
	n := 0
	for i := range s.shards {
		sh := &s.shards[i]
		sh.mu.Lock()
		n += len(sh.m)
		sh.mu.Unlock()
	}
	return n
}

// ---- общие помощники ----

func nanos(t time.Time) int64 { return t.UnixNano() }

func maxI64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// floorDiv — деление с округлением вниз (для моментов до 1970 года).
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
