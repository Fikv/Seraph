package md

import (
	"regexp"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"

	t "seraph/internal/theme"
)

type Doc struct {
	Body  string
	Tags  []string
	Links []string
}

var (
	frontmatter = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n?`)
	embed       = regexp.MustCompile(`!\[\[([^\]|#]+)(?:#[^\]|]*)?(?:\|([^\]]*))?\]\]`)
	wikilink    = regexp.MustCompile(`\[\[([^\]|#]+)?(#[^\]|]*)?(?:\|([^\]]*))?\]\]`)
	callout     = regexp.MustCompile(`(?m)^(>\s*)\[!([A-Za-z-]+)\][+-]?\s*(.*)$`)
	highlight   = regexp.MustCompile(`==([^=\n]+)==`)
	tagLine     = regexp.MustCompile(`(?m)^tags:\s*\[?([^\]\n]*)\]?\s*$`)
)

func Prepare(src string) Doc {
	var d Doc
	src = strings.ReplaceAll(src, "\r\n", "\n")
	if m := frontmatter.FindStringSubmatch(src); m != nil {
		if tm := tagLine.FindStringSubmatch(m[1]); tm != nil {
			for _, tg := range strings.Split(tm[1], ",") {
				if tg = strings.Trim(strings.TrimSpace(tg), `"'`); tg != "" {
					d.Tags = append(d.Tags, tg)
				}
			}
		}
		src = src[len(m[0]):]
	}
	seen := map[string]bool{}
	addLink := func(target string) {
		target = strings.TrimSpace(target)
		if target != "" && !seen[target] {
			seen[target] = true
			d.Links = append(d.Links, target)
		}
	}
	src = embed.ReplaceAllStringFunc(src, func(s string) string {
		m := embed.FindStringSubmatch(s)
		addLink(m[1])
		return "⧉ *" + strings.TrimSpace(firstNonEmpty(m[2], m[1])) + "*"
	})
	src = wikilink.ReplaceAllStringFunc(src, func(s string) string {
		m := wikilink.FindStringSubmatch(s)
		target, heading, alias := m[1], strings.TrimPrefix(m[2], "#"), m[3]
		addLink(target)
		label := firstNonEmpty(alias, target, heading)
		if alias == "" && target != "" && heading != "" {
			label = target + " › " + heading
		}
		return "**" + strings.TrimSpace(label) + "**"
	})
	src = callout.ReplaceAllStringFunc(src, func(s string) string {
		m := callout.FindStringSubmatch(s)
		title := strings.ToUpper(m[2])
		if strings.TrimSpace(m[3]) != "" {
			title += " · " + strings.TrimSpace(m[3])
		}
		return m[1] + "**" + title + "**"
	})
	src = highlight.ReplaceAllString(src, "**$1**")
	d.Body = src
	return d
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func ptr[T any](v T) *T { return &v }

func Style() ansi.StyleConfig {
	s := styles.DarkStyleConfig
	ivory, gold, lav, cyan, dim, faint, mint := t.Ivory.Hex(), t.Gold.Hex(), t.Lavender.Hex(), t.Cyan.Hex(), t.Dim.Hex(), t.Faint.Hex(), t.Mint.Hex()

	s.Document.Margin = ptr(uint(0))
	s.Document.Color = &ivory
	s.Document.BlockPrefix, s.Document.BlockSuffix = "", ""
	s.Paragraph.Color = &ivory

	s.Heading.Color, s.Heading.Bold = &gold, ptr(true)
	s.H1 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Prefix: "✧ ", Color: &gold, Bold: ptr(true), Upper: ptr(true)}}
	s.H2 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Prefix: "✦ ", Color: &gold, Bold: ptr(true)}}
	s.H3 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Prefix: "⟡ ", Color: &lav, Bold: ptr(true)}}
	s.H4 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Prefix: "· ", Color: &lav}}
	s.H5 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Prefix: "· ", Color: &dim}}
	s.H6 = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Prefix: "· ", Color: &dim}}

	s.Strong = ansi.StylePrimitive{Color: &ivory, Bold: ptr(true)}
	s.Emph = ansi.StylePrimitive{Color: &lav, Italic: ptr(true)}
	s.Strikethrough = ansi.StylePrimitive{Color: &dim, CrossedOut: ptr(true)}
	s.HorizontalRule = ansi.StylePrimitive{Color: &faint, Format: "\n────────\n"}

	s.BlockQuote.Color = &dim
	s.BlockQuote.IndentToken = ptr("┃ ")
	s.BlockQuote.Indent = ptr(uint(1))

	s.Item = ansi.StylePrimitive{BlockPrefix: "• ", Color: &ivory}
	s.Enumeration = ansi.StylePrimitive{BlockPrefix: ". ", Color: &gold}
	s.Task.Ticked, s.Task.Unticked = "[✓] ", "[ ] "

	s.Link = ansi.StylePrimitive{Color: &faint, Underline: ptr(true)}
	s.LinkText = ansi.StylePrimitive{Color: &lav, Bold: ptr(true)}
	s.ImageText = ansi.StylePrimitive{Color: &lav, Format: "⧉ {{.text}}"}

	s.Code = ansi.StyleBlock{StylePrimitive: ansi.StylePrimitive{Color: &cyan}}
	s.CodeBlock.Color = &mint
	s.CodeBlock.Margin = ptr(uint(1))
	s.CodeBlock.Theme = "dracula"
	s.Table.Color = &ivory
	return s
}

var (
	mu        sync.Mutex
	renderers = map[int]*glamour.TermRenderer{}
	cache     = map[string]string{}
	order     []string
)

const cacheSize = 400

func renderer(width int) (*glamour.TermRenderer, error) {
	if r, ok := renderers[width]; ok {
		return r, nil
	}
	r, err := glamour.NewTermRenderer(glamour.WithStyles(Style()), glamour.WithWordWrap(width), glamour.WithPreservedNewLines())
	if err != nil {
		return nil, err
	}
	renderers[width] = r
	return r, nil
}

func Render(src string, width int) string {
	width = max(width, 20)
	key := string(rune(width)) + "\x00" + src
	mu.Lock()
	defer mu.Unlock()
	if out, ok := cache[key]; ok {
		return out
	}
	r, err := renderer(width)
	if err != nil {
		return src
	}
	out, err := r.Render(Prepare(src).Body)
	if err != nil {
		return src
	}
	out = strings.Trim(out, "\n")
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	out = strings.Join(lines, "\n")
	cache[key] = out
	order = append(order, key)
	if len(order) > cacheSize {
		delete(cache, order[0])
		order = order[1:]
	}
	return out
}
