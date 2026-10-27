package main

import (
	"math"
	"strconv"
	"testing"
)

// contrast is the WCAG contrast ratio between two #rrggbb colours.
func contrast(a, b string) float64 {
	lum := func(h string) float64 {
		var c [3]float64
		for i := range c {
			v, _ := strconv.ParseUint(h[1+2*i:3+2*i], 16, 8)
			x := float64(v) / 255
			if x <= 0.03928 {
				c[i] = x / 12.92
			} else {
				c[i] = math.Pow((x+0.055)/1.055, 2.4)
			}
		}
		return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
	}
	la, lb := lum(a), lum(b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

// TestFaintLinesAreVisible: tree connectors and header separators use the
// theme's Border colour. It must stand out from the theme's panel background
// and from a typical dark terminal, as the other themes' lines do (1.45+).
func TestFaintLinesAreVisible(t *testing.T) {
	for _, th := range AvailableThemes {
		border := string(th.Border)
		for _, bg := range []string{string(th.BoxBg), "#1e1e1e"} {
			if c := contrast(border, bg); c < 1.4 {
				t.Errorf("%s: Border %s on %s has contrast %.2f; tree lines would vanish", th.Name, border, bg, c)
			}
		}
	}
}
