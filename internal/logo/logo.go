package logo

import (
	"bytes"
	_ "embed"
	"fmt"
	"image/color"
	"image/png"
	"strings"
	"sync"
)

//go:embed logo.png
var raw []byte

type bitmap struct {
	w, h int
	px   []color.NRGBA
	op   []bool
}

var (
	once  sync.Once
	src   *bitmap
	cache sync.Map
)

func load() {
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	full := &bitmap{w: w, h: h, px: make([]color.NRGBA, w*h), op: make([]bool, w*h)}
	cutoff := h * 95 / 100
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			i := y*w + x
			full.px[i] = c
			full.op[i] = c.A >= 128 && y < cutoff
		}
	}
	clearBackground(full)
	src = crop(full)
}

func nearWhite(c color.NRGBA) bool { return c.R >= 250 && c.G >= 250 && c.B >= 250 }

func clearBackground(bm *bitmap) {
	stack := make([]int, 0, 4096)
	push := func(x, y int) {
		if x < 0 || y < 0 || x >= bm.w || y >= bm.h {
			return
		}
		i := y*bm.w + x
		if bm.op[i] && nearWhite(bm.px[i]) {
			bm.op[i] = false
			stack = append(stack, i)
		}
	}
	for x := 0; x < bm.w; x++ {
		push(x, 0)
		push(x, bm.h-1)
	}
	for y := 0; y < bm.h; y++ {
		push(0, y)
		push(bm.w-1, y)
	}
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		x, y := i%bm.w, i/bm.w
		push(x+1, y)
		push(x-1, y)
		push(x, y+1)
		push(x, y-1)
	}
}

func crop(bm *bitmap) *bitmap {
	minX, minY, maxX, maxY := bm.w, bm.h, -1, -1
	for y := 0; y < bm.h; y++ {
		for x := 0; x < bm.w; x++ {
			if bm.op[y*bm.w+x] {
				minX, maxX = min(minX, x), max(maxX, x)
				minY, maxY = min(minY, y), max(maxY, y)
			}
		}
	}
	if maxX < 0 {
		return bm
	}
	w, h := maxX-minX+1, maxY-minY+1
	out := &bitmap{w: w, h: h, px: make([]color.NRGBA, w*h), op: make([]bool, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			si := (y+minY)*bm.w + x + minX
			out.px[y*w+x] = bm.px[si]
			out.op[y*w+x] = bm.op[si]
		}
	}
	return out
}

type cell struct {
	c  color.NRGBA
	ok bool
}

func sample(gw, gh int) []cell {
	grid := make([]cell, gw*gh)
	for gy := 0; gy < gh; gy++ {
		y0, y1 := gy*src.h/gh, max((gy+1)*src.h/gh, gy*src.h/gh+1)
		for gx := 0; gx < gw; gx++ {
			x0, x1 := gx*src.w/gw, max((gx+1)*src.w/gw, gx*src.w/gw+1)
			var r, g, b, n, total int
			for y := y0; y < y1 && y < src.h; y++ {
				for x := x0; x < x1 && x < src.w; x++ {
					total++
					i := y*src.w + x
					if !src.op[i] {
						continue
					}
					p := src.px[i]
					r, g, b, n = r+int(p.R), g+int(p.G), b+int(p.B), n+1
				}
			}
			if total > 0 && n*100/total >= 40 {
				grid[gy*gw+gx] = cell{c: color.NRGBA{uint8(r / n), uint8(g / n), uint8(b / n), 255}, ok: true}
			}
		}
	}
	return grid
}

func Render(maxCols, maxRows int) string {
	once.Do(load)
	if src == nil || maxCols < 6 || maxRows < 3 {
		return ""
	}
	key := fmt.Sprintf("%dx%d", maxCols, maxRows)
	if v, ok := cache.Load(key); ok {
		return v.(string)
	}
	cols := maxCols
	gh := cols * src.h / src.w
	if (gh+1)/2 > maxRows {
		gh = maxRows * 2
		cols = gh * src.w / src.h
	}
	if cols < 6 || gh < 4 {
		return ""
	}
	grid := sample(cols, gh)
	at := func(x, y int) cell {
		if y >= gh {
			return cell{}
		}
		return grid[y*cols+x]
	}
	var b strings.Builder
	for y := 0; y < gh; y += 2 {
		for x := 0; x < cols; x++ {
			top, bot := at(x, y), at(x, y+1)
			switch {
			case top.ok && bot.ok:
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", top.c.R, top.c.G, top.c.B, bot.c.R, bot.c.G, bot.c.B)
			case top.ok:
				fmt.Fprintf(&b, "\x1b[49m\x1b[38;2;%d;%d;%dm▀", top.c.R, top.c.G, top.c.B)
			case bot.ok:
				fmt.Fprintf(&b, "\x1b[49m\x1b[38;2;%d;%d;%dm▄", bot.c.R, bot.c.G, bot.c.B)
			default:
				b.WriteString("\x1b[0m ")
			}
		}
		b.WriteString("\x1b[0m")
		if y+2 < gh {
			b.WriteByte('\n')
		}
	}
	out := b.String()
	cache.Store(key, out)
	return out
}

func Size(maxCols, maxRows int) (int, int) {
	s := Render(maxCols, maxRows)
	if s == "" {
		return 0, 0
	}
	lines := strings.Split(s, "\n")
	return countCells(lines[0]), len(lines)
}

func countCells(line string) int {
	n, esc := 0, false
	for _, r := range line {
		switch {
		case esc:
			if r == 'm' {
				esc = false
			}
		case r == '\x1b':
			esc = true
		default:
			n++
		}
	}
	return n
}
