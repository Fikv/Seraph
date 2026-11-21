package theme

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type RGB struct{ R, G, B uint8 }

func (c RGB) Hex() string { return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B) }

func (c RGB) Color() lipgloss.Color { return lipgloss.Color(c.Hex()) }

var (
	Ivory    = RGB{247, 243, 232}
	Gold     = RGB{244, 213, 141}
	DeepGold = RGB{214, 168, 74}
	Cyan     = RGB{139, 233, 253}
	Lavender = RGB{195, 177, 255}
	Rose     = RGB{255, 122, 144}
	Mint     = RGB{152, 240, 196}
	Dim      = RGB{132, 126, 152}
	Faint    = RGB{72, 68, 92}
	Void     = RGB{14, 13, 24}
)

var Halo = []RGB{Gold, Ivory, Cyan, Lavender, Gold}

var Dawn = []RGB{DeepGold, Gold, Ivory, Cyan, Lavender, DeepGold}

func Lerp(a, b RGB, t float64) RGB {
	t = math.Max(0, math.Min(1, t))
	mix := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return RGB{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B)}
}

func At(stops []RGB, t float64) RGB {
	if len(stops) == 1 {
		return stops[0]
	}
	t -= math.Floor(t)
	seg := t * float64(len(stops)-1)
	i := int(seg)
	if i >= len(stops)-1 {
		i = len(stops) - 2
	}
	return Lerp(stops[i], stops[i+1], seg-float64(i))
}

func Gradient(s string, stops []RGB, phase float64, bold bool) string {
	runes := []rune(s)
	n := len(runes)
	if n == 0 {
		return ""
	}
	var b strings.Builder
	if bold {
		b.WriteString("\x1b[1m")
	}
	for i, r := range runes {
		if r == ' ' {
			b.WriteRune(' ')
			continue
		}
		c := At(stops, float64(i)/float64(n)+phase)
		fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm%c", c.R, c.G, c.B, r)
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

func Pulse(a, b RGB, frame, period int) RGB {
	t := (math.Sin(float64(frame)/float64(period)*2*math.Pi) + 1) / 2
	return Lerp(a, b, t)
}

func FG(c RGB) lipgloss.Style { return lipgloss.NewStyle().Foreground(c.Color()) }
