//go:build js && wasm

// Command wasm запускает лабораторию в браузере: ratelabRun(json) -> json со svg и числами, ratelabPatterns() -> json.
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/Oleg4311/rate-limiter-lab/lab"
)

func run(_ js.Value, args []js.Value) any {
	var s lab.Scenario
	if err := json.Unmarshal([]byte(args[0].String()), &s); err != nil {
		return reply(map[string]any{"error": "некорректные параметры: " + err.Error()})
	}
	res, err := lab.Run(s)
	if err != nil {
		return reply(map[string]any{"error": err.Error()})
	}
	return reply(map[string]any{"svg": lab.SVG(res), "result": res})
}

func reply(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func patterns(_ js.Value, _ []js.Value) any {
	b, _ := json.Marshal(lab.Patterns)
	return string(b)
}

func main() {
	js.Global().Set("ratelabRun", js.FuncOf(run))
	js.Global().Set("ratelabPatterns", js.FuncOf(patterns))
	select {}
}
