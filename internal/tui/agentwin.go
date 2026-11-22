package tui

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf16"

	tea "github.com/charmbracelet/bubbletea"

	"seraph/internal/store"
	t "seraph/internal/theme"
)

const (
	barSeg   = 14
	barSpeed = 42.0
)

var (
	sBarCore = t.FG(t.Cyan).Bold(true)
	sBarEdge = t.FG(t.Lavender)
	sBarBase = t.FG(t.Faint)
)

func workingBar(w int, el time.Duration) string {
	if w <= barSeg+2 {
		return sBarBase.Render(strings.Repeat("━", max(w, 0)))
	}
	span := w - barSeg
	period := 2 * span
	pos := int(el.Seconds()*barSpeed) % period
	if pos > span {
		pos = period - pos
	}
	edge := 3
	return sBarBase.Render(strings.Repeat("━", pos)) +
		sBarEdge.Render(strings.Repeat("▰", edge)) +
		sBarCore.Render(strings.Repeat("▰", barSeg-2*edge)) +
		sBarEdge.Render(strings.Repeat("▰", edge)) +
		sBarBase.Render(strings.Repeat("━", w-pos-barSeg))
}

func (m *Model) detailWorking() bool {
	s, ok := m.mgr.Get(m.openID)
	return ok && (s.Status == store.StatusWorking || s.Status == store.StatusQueued) && m.mgr.Active(s.ID)
}

func (m *Model) toggleSelectMode() tea.Cmd {
	m.selectMode = !m.selectMode
	if m.selectMode {
		m.setFlash("select mode: mouse released, drag to select and copy with your terminal · ctrl+t gives the mouse back", false)
		return tea.DisableMouse
	}
	m.setFlash("mouse back in seraph", false)
	return tea.EnableMouseAllMotion
}

func (m *Model) leaveSelectMode() tea.Cmd {
	if !m.selectMode {
		return nil
	}
	m.selectMode = false
	return tea.EnableMouseAllMotion
}

func (m *Model) copyLastAnswer() {
	text := strings.TrimSpace(m.mgr.ResultText(m.openID))
	if text == "" {
		m.setFlash("nothing to copy yet: the agent has not answered", true)
		return
	}
	if err := copyToClipboard(text); err != nil {
		m.setFlash("copy failed: "+err.Error(), true)
		return
	}
	m.setFlash(fmt.Sprintf("copied the last answer (%d chars) to the clipboard", len([]rune(text))), false)
}

func copyToClipboard(text string) error {
	if p, err := exec.LookPath("clip.exe"); err == nil {
		units := utf16.Encode([]rune(text))
		buf := bytes.NewBuffer([]byte{0xFF, 0xFE})
		for _, u := range units {
			buf.WriteByte(byte(u))
			buf.WriteByte(byte(u >> 8))
		}
		cmd := exec.Command(p)
		cmd.Stdin = buf
		return cmd.Run()
	}
	for _, c := range [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"pbcopy"}} {
		if p, err := exec.LookPath(c[0]); err == nil {
			cmd := exec.Command(p, c[1:]...)
			cmd.Stdin = strings.NewReader(text)
			return cmd.Run()
		}
	}
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return errors.New("no clipboard tool found")
	}
	defer tty.Close()
	_, err = fmt.Fprintf(tty, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text)))
	return err
}

func (m *Model) currentPermission() (store.Permission, bool) {
	if m.scr == scrSplash {
		return store.Permission{}, false
	}
	for _, p := range m.stats.permissions {
		if until, ok := m.permitSnooze[p.ID]; ok && time.Now().Before(until) {
			continue
		}
		return p, true
	}
	return store.Permission{}, false
}

func (m *Model) permissionView(p store.Permission) string {
	w := m.modalWidth() - 4
	who := p.SessionID
	if s, ok := m.mgr.Get(p.SessionID); ok {
		who = agentGlyph(s.Agent) + " " + sIvory.Render(trunc(s.Title, w-12)) + sFaint.Render(" · "+s.ID)
	}
	what := p.Summary()
	var body []string
	body = append(body, sDim.Render("worker ")+who, "")
	body = append(body, sGoldB.Render(p.Tool)+sDim.Render(" wants to run:"))
	for _, l := range strings.Split(what, "\n") {
		for len([]rune(l)) > w-4 {
			body = append(body, "  "+sIvory.Render(string([]rune(l)[:w-4])))
			l = string([]rune(l)[w-4:])
		}
		body = append(body, "  "+sIvory.Render(l))
	}
	if d, ok := p.Input["description"].(string); ok && d != "" {
		body = append(body, "", sFaint.Render(trunc("why: "+d, w)))
	}
	always := "always allow " + p.Tool + " in this worker"
	if p.Tool == "Bash" {
		always = "always allow `" + store.CommandPrefix(p.Command()) + " …` in this worker"
	}
	body = fitLines(body, 0, max(m.h-12, 5))
	more := ""
	if n := len(m.stats.permissions); n > 1 {
		more = sFaint.Render(fmt.Sprintf("  (%d more waiting)", n-1))
	}
	text := strings.Join(body, "\n") + "\n\n" +
		hints(w, m.chip("y", "y", "allow once"), m.chip("a", "a", trunc(always, 40)), m.chip("n", "n", "deny")) + "\n" +
		hints(w, m.chip("enter", "⏎", "open worker"), m.chip("esc", "esc", "later")) + more
	return modal("⚿ PERMISSION", text, m.modalWidth())
}

func (m *Model) permissionKey(p store.Permission, k tea.KeyMsg) (tea.Model, tea.Cmd) {
	var err error
	switch k.String() {
	case "y":
		err = m.mgr.DecidePermission(p.ID, true, false, "")
	case "a":
		err = m.mgr.DecidePermission(p.ID, true, true, "")
	case "n":
		err = m.mgr.DecidePermission(p.ID, false, false, "")
	case "enter":
		m.permitSnooze[p.ID] = time.Now().Add(8 * time.Second)
		if m.scr != scrDetail || m.openID != p.SessionID {
			m.backTo = m.scr
			if m.backTo == scrDetail {
				m.backTo = scrBoard
			}
			m.openDetail(p.SessionID)
		}
		return m, nil
	case "esc":
		m.permitSnooze[p.ID] = time.Now().Add(20 * time.Second)
		return m, nil
	default:
		return m, nil
	}
	if err != nil {
		m.setFlash(err.Error(), true)
	}
	m.refreshPermissions()
	return m, nil
}

func (m *Model) refreshPermissions() {
	m.stats.permissions = m.mgr.PendingPermissions()
	m.stats.waitingFor = map[string]string{}
	for _, p := range m.stats.permissions {
		if _, ok := m.stats.waitingFor[p.SessionID]; !ok {
			m.stats.waitingFor[p.SessionID] = p.Tool + ": " + p.Summary()
		}
	}
	m.permAt = time.Now()
}
