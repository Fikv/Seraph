package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"seraph/internal/agent"
	"seraph/internal/orch"
	"seraph/internal/store"
)

func testModel(t *testing.T) *Model {
	t.Helper()
	t.Setenv("SERAPH_HOME", t.TempDir())
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i, s := range []store.Session{
		{Title: "Add a very long title that keeps going and going well past any reasonable card width", Agent: "claude", Status: store.StatusDone, Branch: "seraph/aaaaaa", LastLine: "\tindented\tsummary with tabs that is definitely wider than the card", Cost: 0.1234},
		{Title: "Fix the parser", Agent: "cursor", Status: store.StatusFailed, Error: "exit status 1: something went terribly wrong in a long error message"},
		{Title: "Stopped worker", Agent: "claude", Status: store.StatusStopped},
		{Title: "Another done", Agent: "cursor", Status: store.StatusDone},
	} {
		s.ID = fmt.Sprintf("s%05d", i)
		s.Task = s.Title
		s.Approval = "safe"
		s.Dir = "/tmp/x"
		s.CreatedAt = now.Add(-time.Duration(i) * time.Hour)
		s.UpdatedAt = s.CreatedAt
		if err := st.Save(s); err != nil {
			t.Fatal(err)
		}
		_ = st.AppendLog(s.ID, agent.Event{Kind: agent.KindUser, Text: "do the thing " + strings.Repeat("word ", 40)})
		_ = st.AppendLog(s.ID, agent.Event{Kind: agent.KindToolStart, Tool: "Bash", Text: strings.Repeat("x", 300)})
		_ = st.AppendLog(s.ID, agent.Event{Kind: agent.KindText, Text: "done\n\n```go\nfunc main() {\n\tif x {\n\t\treturn\t// " + strings.Repeat("comment ", 9) + "\n\t}\n}\n```\r\nprogress 50%\rprogress 100%\n" + strings.Repeat("long line ", 50)})
		_ = st.AppendLog(s.ID, agent.Event{Kind: agent.KindResult, Cost: 0.01})
	}
	mgr, err := orch.New(st)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(mgr, Options{Repo: "/tmp/repo", Dir: "/tmp/repo", Agent: "claude", Approval: agent.ApprovalSafe, Version: "0.1.0"})
	m.versions = map[string]string{"claude": "2.1.330", "cursor": "2026.11.19-f005e9d"}
	m.plan = []orch.PlannedTask{
		{Title: "First task with a long title " + strings.Repeat("a", 80), Task: strings.Repeat("do stuff ", 60), Agent: "claude"},
		{Title: "Second", Task: "short", Agent: "cursor"},
	}
	m.planGoal = strings.Repeat("goal ", 50)
	m.planStart = time.Now()
	m.confirmID = "s00000"
	return m
}

func TestLayoutFitsTerminal(t *testing.T) {
	zone.NewGlobal()
	sizes := [][2]int{{60, 20}, {80, 24}, {100, 30}, {120, 40}, {160, 45}, {236, 64}}
	screens := map[string]func(m *Model){
		"splash-anim": func(m *Model) {
			m.scr = scrSplash
			m.splashStart = time.Now().Add(-700 * time.Millisecond)
			m.splashDone = false
		},
		"splash":   func(m *Model) { m.scr = scrSplash; m.splashDone = true },
		"board":    func(m *Model) { m.scr = scrBoard },
		"detail":   func(m *Model) { m.openDetail("s00000") },
		"new":      func(m *Model) { m.scr = scrNew },
		"plangoal": func(m *Model) { m.scr = scrPlanGoal },
		"planning": func(m *Model) { m.scr = scrPlanning },
		"review":   func(m *Model) { m.scr = scrPlanReview },
		"confirm":  func(m *Model) { m.scr = scrConfirm },
		"menu":     func(m *Model) { m.scr = scrMenu },
	}
	for _, sz := range sizes {
		for name, set := range screens {
			m := testModel(t)
			m.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
			set(m)
			out := m.View()
			lines := strings.Split(out, "\n")
			if len(lines) > sz[1] {
				t.Errorf("%s @%dx%d: %d lines > height", name, sz[0], sz[1], len(lines))
			}
			for i, l := range lines {
				if plain := ansi.Strip(l); strings.ContainsAny(plain, "\t\r\b\x00\x7f") {
					t.Errorf("%s @%dx%d: line %d has raw control chars: %q", name, sz[0], sz[1], i, plain)
					break
				}
				if w := ansi.StringWidth(l); w > sz[0] {
					t.Errorf("%s @%dx%d: line %d width %d > %d: %q", name, sz[0], sz[1], i, w, sz[0], ansi.Strip(l))
					break
				}
			}
		}
	}
}
