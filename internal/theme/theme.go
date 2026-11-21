package theme

import (
	"fmt"
	"math"

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

func Lerp(a, b RGB, t float64) RGB {
	t = math.Max(0, math.Min(1, t))
	mix := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return RGB{mix(a.R, b.R), mix(a.G, b.G), mix(a.B, b.B)}
}

func FG(c RGB) lipgloss.Style { return lipgloss.NewStyle().Foreground(c.Color()) }
