package lab

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

const (
	colAllowed = "#1f6fb2" // синий
	colDenied  = "#d95f02" // оранжевый: пара различима при дальтонизме
	colInk     = "#1b1f24"
	colMuted   = "#5b6573"
)

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func ru(f float64, digits int) string {
	return strings.Replace(strconv.FormatFloat(f, 'f', digits, 64), ".", ",", 1)
}

// SVG рисует по одному ряду столбцов на алгоритм: синее — принято, оранжевое — отклонено.
// Картинка самодостаточна (белый фон, фиксированные цвета), поэтому одинаково читается в светлой и тёмной теме.
func SVG(r Result) string {
	const (
		w       = 760
		left    = 20
		plotW   = w - 2*left
		rowH    = 96
		plotH   = 52
		headerH = 58
	)
	h := headerH + rowH*len(r.Algorithm) + 28
	var b strings.Builder
	title := fmt.Sprintf("Сравнение алгоритмов: лимит %d за %s мс, предложено запросов: %d", r.Scenario.Limit, strconv.Itoa(r.Scenario.WindowMs), r.Offered)
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" role="img" aria-label="%s" font-family="system-ui,-apple-system,Segoe UI,Roboto,sans-serif">`, w, h, w, esc(title))
	fmt.Fprintf(&b, `<title>%s</title><rect width="%d" height="%d" rx="12" fill="#ffffff"/>`, esc(title), w, h)
	fmt.Fprintf(&b, `<text x="%d" y="26" font-size="16" font-weight="700" fill="%s">%s</text>`, left, colInk, esc(patternTitle(r.Scenario.Pattern)))
	fmt.Fprintf(&b, `<text x="%d" y="46" font-size="12" fill="%s">лимит %d за %s мс · предложено %d запросов · один столбец = %s мс</text>`,
		left, colMuted, r.Scenario.Limit, strconv.Itoa(r.Scenario.WindowMs), r.Offered, ru(r.BucketMs, 0))
	// легенда
	fmt.Fprintf(&b, `<rect x="%d" y="34" width="10" height="10" fill="%s"/><text x="%d" y="43" font-size="12" fill="%s">принято</text>`, w-210, colAllowed, w-194, colMuted)
	fmt.Fprintf(&b, `<rect x="%d" y="34" width="10" height="10" fill="%s"/><text x="%d" y="43" font-size="12" fill="%s">отклонено</text>`, w-130, colDenied, w-114, colMuted)

	peak := 1
	for _, a := range r.Algorithm {
		for i := range a.AllowedByBucket {
			if v := a.AllowedByBucket[i] + a.DeniedByBucket[i]; v > peak {
				peak = v
			}
		}
	}
	bw := float64(plotW) / float64(r.Buckets)
	for row, a := range r.Algorithm {
		top := headerH + row*rowH
		ratio := float64(a.MaxInWindow) / float64(r.Scenario.Limit)
		warn := ratio > 1.0001
		verdict := "не больше лимита"
		if warn {
			verdict = "выше лимита в " + ru(ratio, 2) + " раза"
		}
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="13" font-weight="700" fill="%s">%s</text>`, left, top+14, colInk, esc(a.Name))
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="12" fill="%s">принято %d, отклонено %d · максимум в окне: %d (%s)</text>`,
			left+130, top+14, colMuted, a.Allowed, a.Denied, a.MaxInWindow, esc(verdict))
		base := float64(top + 20 + plotH)
		fmt.Fprintf(&b, `<line x1="%d" y1="%.1f" x2="%d" y2="%.1f" stroke="#c9d1db"/>`, left, base, left+plotW, base)
		for i := 0; i < r.Buckets; i++ {
			x := float64(left) + float64(i)*bw
			ha := float64(a.AllowedByBucket[i]) / float64(peak) * plotH
			hd := float64(a.DeniedByBucket[i]) / float64(peak) * plotH
			if ha > 0 {
				fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s"/>`, x, base-ha, bw-0.6, ha, colAllowed)
			}
			if hd > 0 {
				fmt.Fprintf(&b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s"/>`, x, base-ha-hd, bw-0.6, hd, colDenied)
			}
		}
	}
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" fill="%s">0</text>`, left, h-10, colMuted)
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-size="11" text-anchor="end" fill="%s">%s с</text>`, left+plotW, h-10, colMuted, ru(float64(r.Scenario.DurationMs)/1000, 1))
	b.WriteString(`</svg>`)
	return b.String()
}

func patternTitle(p Pattern) string {
	for _, x := range Patterns {
		if x.ID == p {
			return x.Title
		}
	}
	return string(p)
}
