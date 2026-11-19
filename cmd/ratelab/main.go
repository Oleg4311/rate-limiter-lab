// Command ratelab сравнивает алгоритмы ограничения частоты и запускает демонстрационный сервис.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Oleg4311/rate-limiter-lab/lab"
	"github.com/Oleg4311/rate-limiter-lab/limiter"
	"github.com/Oleg4311/rate-limiter-lab/service"
)

var version = "0.1.0"

const usage = `ratelab — лаборатория ограничения частоты запросов

Использование:
  ratelab compare [параметры]   прогнать все алгоритмы на одном трафике и сравнить
  ratelab serve   [параметры]   запустить сервис: GET /limited/{алгоритм}, /stats, /healthz
  ratelab version

Алгоритмы: ` + "fixed-window, sliding-log, sliding-counter, token-bucket, gcra" + `
Паттерны трафика: steady, burst, boundary, poisson, ramp`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "compare":
		err = compare(os.Args[2:])
	case "serve":
		err = serve(os.Args[2:])
	case "version", "--version":
		fmt.Println("ratelab", version)
	case "help", "--help", "-h":
		fmt.Println(usage)
	default:
		err = fmt.Errorf("неизвестная команда %q\n\n%s", os.Args[1], usage)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ratelab:", err)
		os.Exit(1)
	}
}

func compare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	pattern := fs.String("pattern", "boundary", "паттерн трафика")
	limit := fs.Int("limit", 100, "лимит запросов за окно")
	window := fs.Duration("window", time.Second, "длина окна")
	burst := fs.Int("burst", 0, "пачка для token-bucket и gcra (0 = limit)")
	rate := fs.Float64("rate", 200, "предлагаемая частота, запросов в секунду (steady, poisson, ramp)")
	duration := fs.Duration("duration", 6*time.Second, "длительность эксперимента")
	seed := fs.Int64("seed", 1, "seed для poisson")
	svgPath := fs.String("svg", "", "сохранить график в SVG")
	asJSON := fs.Bool("json", false, "вывести результат в JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	res, err := lab.Run(lab.Scenario{Pattern: lab.Pattern(*pattern), Limit: *limit, Window: *window, Burst: *burst, Rate: *rate, Duration: *duration, Seed: *seed})
	if err != nil {
		return err
	}
	if *svgPath != "" {
		if err := os.WriteFile(*svgPath, []byte(lab.SVG(res)), 0o644); err != nil {
			return err
		}
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	fmt.Printf("Паттерн: %s · лимит %d за %v · предложено %d запросов\n\n", *pattern, *limit, *window, res.Offered)
	fmt.Printf("%-16s %8s %10s %14s  %s\n", "алгоритм", "принято", "отклонено", "макс. в окне", "относительно лимита")
	for _, a := range res.Algorithm {
		ratio := float64(a.MaxInWindow) / float64(*limit)
		note := "не больше лимита"
		if ratio > 1.0001 {
			note = strings.Replace(fmt.Sprintf("выше лимита в %.2f раза", ratio), ".", ",", 1)
		}
		fmt.Printf("%-16s %8d %10d %14d  %s\n", a.Name, a.Allowed, a.Denied, a.MaxInWindow, note)
	}
	return nil
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "адрес для прослушивания")
	limit := fs.Int("limit", 100, "лимит запросов за окно")
	window := fs.Duration("window", time.Second, "длина окна")
	burst := fs.Int("burst", 0, "пачка для token-bucket и gcra (0 = limit)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	svc, err := service.New(limiter.Config{Limit: *limit, Window: *window, Burst: *burst}, nil)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: *addr, Handler: svc.Handler, ReadHeaderTimeout: 5 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				svc.SweepAll(time.Now())
			case <-stop:
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = srv.Shutdown(ctx)
				return
			}
		}
	}()
	log.Printf("ratelab %s: слушаю %s, лимит %d за %v", version, *addr, *limit, *window)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	log.Println("остановлен")
	return nil
}
