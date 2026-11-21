package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"seraph/internal/md"
	"seraph/internal/obsidian"
)

type noteFile struct {
	Source string
	Kind   string
	Path   string
	Rel    string
	Mod    int64
}

type notesState struct {
	files     []noteFile
	view      []int
	sel       int
	filter    textinput.Model
	filtering bool
	focus     int
	vp        viewport.Model
	shown     string
	shownW    int
	links     []string
	tags      []string
	linkPick  bool
	linkSel   int
}

const notesListLimit = 3000

func (m *Model) noteSources() []noteFile {
	var out []noteFile
	add := func(source, kind, dir string) {
		for _, n := range obsidian.List(dir, notesListLimit) {
			out = append(out, noteFile{Source: source, Kind: kind, Path: n.Path, Rel: n.Rel, Mod: n.ModTime.Unix()})
		}
	}
	if m.project != nil {
		if m.project.NotesDir != "" {
			add("notes · "+m.project.Name, "notes", m.project.NotesDir)
		}
		for _, r := range m.project.Repos {
			add("repo · "+filepath.Base(r), "repo", r)
		}
		return out
	}
	if r := m.spawnRepo(); r != "" {
		add("repo · "+filepath.Base(r), "repo", r)
	}
	return out
}

func (m *Model) openNotesBrowser() tea.Cmd {
	if m.notes == nil {
		in := textinput.New()
		in.Prompt = "/ "
		in.PromptStyle = sGold
		in.TextStyle = sIvory
		in.Placeholder = "filter notes"
		in.PlaceholderStyle = sFaint
		m.notes = &notesState{filter: in, vp: viewport.New(40, 10)}
	}
	n := m.notes
	n.files = m.noteSources()
	n.linkPick = false
	m.applyNoteFilter()
	if len(n.files) == 0 {
		if m.project == nil {
			m.setFlash("no markdown here: pick a project with notes (g) or run seraph in a repo", true)
		} else {
			m.setFlash("project "+m.project.Name+" has no notes folder or markdown files: press g, then e", true)
		}
		return nil
	}
	m.scr = scrNotes
	if n.shown == "" {
		for i, idx := range n.view {
			if n.files[idx].Kind == "notes" && strings.EqualFold(filepath.Base(n.files[idx].Rel), m.projectName()) {
				n.sel = i
			}
		}
	}
	m.loadNotePreview(true)
	return nil
}

func (m *Model) projectName() string {
	if m.project == nil {
		return ""
	}
	return m.project.Name
}

func (m *Model) applyNoteFilter() {
	n := m.notes
	q := strings.ToLower(strings.TrimSpace(n.filter.Value()))
	n.view = n.view[:0]
	for i, f := range n.files {
		if q == "" || strings.Contains(strings.ToLower(f.Rel), q) || strings.Contains(strings.ToLower(f.Source), q) {
			n.view = append(n.view, i)
		}
	}
	n.sel = min(n.sel, max(len(n.view)-1, 0))
}

func (m *Model) currentNote() (noteFile, bool) {
	n := m.notes
	if n == nil || len(n.view) == 0 {
		return noteFile{}, false
	}
	return n.files[n.view[n.sel]], true
}

func (m *Model) notesLayout() (listW, prevW, bodyH int) {
	bodyH = max(m.h-7, 4)
	listW = min(max(m.w/3, 30), 52)
	prevW = m.w - listW - 3
	if prevW < 30 {
		return m.w, 0, bodyH
	}
	return listW, prevW, bodyH
}

func (m *Model) loadNotePreview(force bool) {
	n := m.notes
	f, ok := m.currentNote()
	_, prevW, bodyH := m.notesLayout()
	if prevW == 0 {
		prevW = m.w
	}
	n.vp.Width, n.vp.Height = prevW, max(bodyH-2, 3)
	if !ok || (!force && f.Path == n.shown && n.shownW == prevW) {
		return
	}
	data, err := os.ReadFile(f.Path)
	if err != nil {
		n.vp.SetContent(sRose.Render("✗ " + err.Error()))
		n.shown, n.links, n.tags = f.Path, nil, nil
		return
	}
	if len(data) > 1<<20 {
		data = data[:1<<20]
	}
	doc := md.Prepare(string(data))
	n.links, n.tags = doc.Links, doc.Tags
	n.vp.SetContent(md.Render(string(data), prevW-1))
	n.vp.GotoTop()
	n.shown, n.shownW = f.Path, prevW
}

func (m *Model) notesKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := m.notes
	key := k.String()
	if n.linkPick {
		switch key {
		case "esc", "l":
			n.linkPick = false
		case "up", "k":
			n.linkSel = max(n.linkSel-1, 0)
		case "down", "j":
			n.linkSel = min(n.linkSel+1, len(n.links)-1)
		case "enter":
			m.followLink(n.links[n.linkSel])
		}
		return m, nil
	}
	if n.filtering {
		switch key {
		case "esc":
			n.filter.SetValue("")
			n.filtering = false
			n.filter.Blur()
		case "enter", "down", "up":
			n.filtering = false
			n.filter.Blur()
		default:
			var cmd tea.Cmd
			n.filter, cmd = n.filter.Update(k)
			m.applyNoteFilter()
			m.loadNotePreview(false)
			return m, cmd
		}
		m.applyNoteFilter()
		m.loadNotePreview(false)
		return m, nil
	}
	if n.focus == 1 {
		switch key {
		case "tab", "esc", "left", "h":
			n.focus = 0
			return m, nil
		case "home", "g":
			n.vp.GotoTop()
			return m, nil
		case "end", "G":
			n.vp.GotoBottom()
			return m, nil
		case "l", "o", "/":
		default:
			var cmd tea.Cmd
			n.vp, cmd = n.vp.Update(k)
			return m, cmd
		}
	}
	switch key {
	case "esc":
		if n.filter.Value() != "" {
			n.filter.SetValue("")
			m.applyNoteFilter()
			m.loadNotePreview(false)
			return m, nil
		}
		m.scr = scrBoard
	case "up", "k":
		if n.sel > 0 {
			n.sel--
			m.loadNotePreview(false)
		}
	case "down", "j":
		if n.sel < len(n.view)-1 {
			n.sel++
			m.loadNotePreview(false)
		}
	case "pgdown":
		n.vp.HalfPageDown()
	case "pgup":
		n.vp.HalfPageUp()
	case "enter", "tab", "right":
		n.focus = 1
	case "/":
		n.filtering = true
		return m, n.filter.Focus()
	case "l":
		if len(n.links) == 0 {
			m.setFlash("this note has no [[links]]", true)
			return m, nil
		}
		n.linkPick, n.linkSel = true, 0
	case "o":
		f, ok := m.currentNote()
		if !ok {
			return m, nil
		}
		if f.Kind != "notes" {
			m.setFlash("only notes from the Obsidian folder open in Obsidian", true)
			return m, nil
		}
		if err := obsidian.Open(f.Path); err != nil {
			m.setFlash("obsidian: "+err.Error(), true)
		} else {
			m.setFlash("opening "+f.Rel+" in Obsidian", false)
		}
	case "r":
		sel := m.notes.shown
		m.notes.files = m.noteSources()
		m.applyNoteFilter()
		for i, idx := range n.view {
			if n.files[idx].Path == sel {
				n.sel = i
			}
		}
		m.loadNotePreview(true)
		m.setFlash(fmt.Sprintf("%d markdown files", len(n.files)), false)
	}
	return m, nil
}

func (m *Model) followLink(target string) {
	n := m.notes
	n.linkPick = false
	want := strings.ToLower(strings.TrimSuffix(target, ".md"))
	for pass := 0; pass < 2; pass++ {
		for i, f := range n.files {
			rel := strings.ToLower(f.Rel)
			if (pass == 0 && rel == want) || (pass == 1 && strings.ToLower(filepath.Base(f.Rel)) == filepath.Base(want)) {
				if n.filter.Value() != "" {
					n.filter.SetValue("")
					m.applyNoteFilter()
				}
				for vi, idx := range n.view {
					if idx == i {
						n.sel = vi
					}
				}
				m.loadNotePreview(true)
				return
			}
		}
	}
	m.setFlash("no note named "+target, true)
}

func (m *Model) notesClick(arg string) (tea.Model, tea.Cmd) {
	n := m.notes
	var i int
	fmt.Sscanf(arg, "%d", &i)
	if i < 0 || i >= len(n.view) {
		return m, nil
	}
	if i == n.sel {
		n.focus = 1
		return m, nil
	}
	n.sel, n.focus = i, 0
	m.loadNotePreview(false)
	return m, nil
}

func (m *Model) notesView() string {
	n := m.notes
	listW, prevW, bodyH := m.notesLayout()
	m.loadNotePreview(false)

	var rows []string
	if n.filtering || n.filter.Value() != "" {
		rows = append(rows, n.filter.View())
	} else {
		rows = append(rows, sFaint.Render(fmt.Sprintf("%d files · / to filter", len(n.files))))
	}
	visible := max(bodyH-2, 1)
	off := 0
	if n.sel >= visible-1 {
		off = n.sel - visible + 2
	}
	lastSource := ""
	if off > 0 && off < len(n.view) {
		lastSource = n.files[n.view[off-1]].Source
	}
	for vi := off; vi < len(n.view) && len(rows) < bodyH; vi++ {
		f := n.files[n.view[vi]]
		if f.Source != lastSource {
			glyph := "◈ "
			if f.Kind == "repo" {
				glyph = "⎇ "
			}
			rows = append(rows, sLavender.Bold(true).Render(trunc(glyph+f.Source, listW)))
			lastSource = f.Source
			if len(rows) >= bodyH {
				break
			}
		}
		dir, base := filepath.Split(f.Rel)
		cursor, style := "  ", sIvory
		if vi == n.sel {
			cursor, style = sGold.Render("› "), sGoldB
			if n.focus == 1 {
				style = sGold
			}
		} else if m.hover == fmt.Sprintf("note:%d", vi) {
			style = sLavender
		}
		label := sFaint.Render(trunc(dir, max(listW-len([]rune(base))-4, 0))) + style.Render(trunc(base, listW-4))
		rows = append(rows, m.mark(fmt.Sprintf("note:%d", vi), cursor+label))
	}
	list := lipgloss.NewStyle().Width(listW).Render(strings.Join(rows, "\n"))

	preview := ""
	if prevW > 0 {
		f, _ := m.currentNote()
		head := sGoldB.Render(trunc(filepath.Base(f.Rel), prevW-20))
		if len(n.tags) > 0 {
			head += sFaint.Render("  #" + strings.Join(n.tags, " #"))
		}
		if n.vp.TotalLineCount() > n.vp.Height {
			head += sFaint.Render(fmt.Sprintf("  %d%%", int(n.vp.ScrollPercent()*100)))
		}
		border := sFaint
		if n.focus == 1 {
			border = sGold
		}
		preview = lipgloss.NewStyle().Width(prevW).Render(trunc(head, prevW) + "\n" + border.Render(strings.Repeat("─", prevW)) + "\n" + m.mark("notesprev", n.vp.View()))
	}
	body := joinPanels(list, preview, prevW, bodyH)

	title := sGoldB.Render("◈ NOTES")
	if m.project != nil {
		title += sFaint.Render(" · ") + sLavender.Render(m.project.Name)
	}
	keys := hints(m.w, m.chip("enter", "⏎", "read"), m.chip("/", "/", "filter"), m.chip("l", "l", "links"),
		m.chip("o", "o", "obsidian"), m.chip("r", "r", "reload"), key("↑↓ pgup pgdn", "scroll"), m.chip("esc", "esc", "back"))
	screen := m.header() + "\n" + title + "\n" + rule(m.w) + "\n" +
		lipgloss.NewStyle().Height(bodyH).MaxHeight(bodyH).Render(body) + "\n" + m.flashLine() + "\n" + keys
	if n.linkPick {
		return m.overlay(m.linkPickerView())
	}
	return screen
}

func (m *Model) linkPickerView() string {
	n := m.notes
	var rows []string
	for i, l := range n.links {
		cursor, style := "  ", sIvory
		if i == n.linkSel {
			cursor, style = sGold.Render("› "), sGoldB
		}
		rows = append(rows, m.mark(fmt.Sprintf("notelink:%d", i), cursor+style.Render(trunc(l, m.modalWidth()-10))))
	}
	rows = fitLines(rows, n.linkSel, m.formLimit()-2)
	body := strings.Join(rows, "\n") + "\n\n" + hints(m.modalWidth()-6, m.chip("enter", "⏎", "open"), m.chip("esc", "esc", "close"))
	return modal("⟡ LINKS IN THIS NOTE", body, min(m.modalWidth(), 70))
}
