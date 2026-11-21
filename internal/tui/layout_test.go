package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone"

	"seraph/internal/agent"
	"seraph/internal/mcp"
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
		_, _ = st.AppendLog(s.ID, agent.Event{Kind: agent.KindUser, Text: "do the thing " + strings.Repeat("word ", 40)})
		_, _ = st.AppendLog(s.ID, agent.Event{Kind: agent.KindToolStart, Tool: "Bash", Text: strings.Repeat("x", 300)})
		_, _ = st.AppendLog(s.ID, agent.Event{Kind: agent.KindText, Text: "done\n\n```go\nfunc main() {\n\tif x {\n\t\treturn\t// " + strings.Repeat("comment ", 9) + "\n\t}\n}\n```\r\nprogress 50%\rprogress 100%\n" + strings.Repeat("long line ", 50)})
		_, _ = st.AppendLog(s.ID, agent.Event{Kind: agent.KindResult, Cost: 0.01})
	}
	mgr, err := orch.New(st, nil)
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
	t.Setenv("HOME", t.TempDir())
	tools := make([]string, 40)
	for i := range tools {
		tools[i] = fmt.Sprintf("tool_number_%d", i)
	}
	m.mcp = &mcpState{search: newSearchInput(), busy: map[string]string{"beta": "testing"}, cfg: &mcp.Config{Servers: []mcp.Server{
		{Name: "alpha", Transport: "stdio", Command: "npx", Args: []string{"-y", "@scope/" + strings.Repeat("very-long-package-", 5)}, Env: map[string]string{"API_KEY": "secret", "ROOT": strings.Repeat("/deep", 30)}, Secrets: []string{"API_KEY"}, Claude: true, Cursor: true, Status: "ok", Tools: tools, Description: strings.Repeat("describes things ", 20), CheckedAt: time.Now()},
		{Name: "beta", Transport: "http", URL: "https://example.com/mcp", Headers: map[string]string{"Authorization": "x"}, Claude: true},
		{Name: "gamma-with-a-really-long-name-that-overflows", Transport: "stdio", Command: "uvx", Status: "error", Error: strings.Repeat("traceback line\t\r", 30)},
	}}}
	entry := mcp.Entry{Name: "io.github.acme/" + strings.Repeat("server", 10), Title: "Acme", Version: "1.0.0", Description: strings.Repeat("a registry description ", 15)}
	entry.Packages = []mcp.Package{{RegistryType: "npm", Identifier: "@acme/x", Version: "1.0.0"}}
	for i := 0; i < 9; i++ {
		entry.Packages[0].EnvironmentVariables = append(entry.Packages[0].EnvironmentVariables, mcp.Input{Name: fmt.Sprintf("VAR_%d", i), Description: strings.Repeat("explains the variable ", 8), IsRequired: i%2 == 0, IsSecret: i%3 == 0})
	}
	entry.Remotes = []mcp.Remote{{Type: "streamable-http", URL: "https://{tenant}.acme.dev/mcp"}}
	for i := 0; i < 12; i++ {
		m.mcp.results = append(m.mcp.results, entry)
	}
	m.mcp.next = "cursor"
	m.mcp.searched = true
	repo := gitRepoForTest(t)
	notes := t.TempDir()
	os.WriteFile(filepath.Join(notes, "Architecture.md"), []byte("x"), 0o644)
	proj := store.Project{ID: "p1", Name: "A project with a rather long name", Description: strings.Repeat("describes the project ", 10), NotesDir: notes, Repos: []string{repo, "/missing/repo/" + strings.Repeat("deep/", 12)}, MaxParallel: 2}
	if err := st.SaveProject(proj); err != nil {
		t.Fatal(err)
	}
	for i, status := range store.TodoStatuses {
		for j := 0; j < 3; j++ {
			sid := ""
			if status == store.TodoDoing || status == store.TodoReview {
				sid = "s00000"
			}
			st.SaveTodo(store.Todo{ID: fmt.Sprintf("t%d%d", i, j), ProjectID: "p1", Title: strings.Repeat("todo title ", 6), Body: strings.Repeat("details ", 30), Status: status, Priority: j, SessionID: sid, Note: strings.Repeat("note ", 20)})
		}
	}
	m.loadProjects()
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
		"splash":        func(m *Model) { m.scr = scrSplash; m.splashDone = true },
		"board":         func(m *Model) { m.scr = scrBoard },
		"detail":        func(m *Model) { m.openDetail("s00000") },
		"new":           func(m *Model) { m.scr = scrNew },
		"plangoal":      func(m *Model) { m.scr = scrPlanGoal },
		"planning":      func(m *Model) { m.scr = scrPlanning },
		"review":        func(m *Model) { m.scr = scrPlanReview },
		"confirm":       func(m *Model) { m.scr = scrConfirm },
		"menu":          func(m *Model) { m.scr = scrMenu },
		"mcp-installed": func(m *Model) { m.scr = scrMCP; m.mcp.tab = 0 },
		"mcp-browse":    func(m *Model) { m.scr = scrMCP; m.mcp.tab = 1; m.mcp.resSel = 7 },
		"mcp-form": func(m *Model) {
			m.scr = scrMCP
			m.mcp.tab = 1
			m.openForm(m.mcp.results[0])
			m.mcp.form.focus = 6
			m.mcp.form.err = strings.Repeat("error ", 40)
		},
		"projects": func(m *Model) { m.openProjects() },
		"project-form": func(m *Model) {
			m.openProjects()
			m.openProjectForm(&m.projects[0])
			m.projForm.err = strings.Repeat("err ", 40)
		},
		"todos": func(m *Model) {
			p := m.projects[0]
			m.useProject(&p)
			m.openTodos()
			m.todoCol = 2
		},
		"todo-form": func(m *Model) {
			p := m.projects[0]
			m.useProject(&p)
			m.openTodos()
			td := m.todos[0]
			m.openTodoForm(&td)
			m.todoForm.err = strings.Repeat("err ", 40)
		},
		"board-project": func(m *Model) { p := m.projects[0]; m.useProject(&p); m.scr = scrBoard },
	}
	if len(screens) != 18 {
		t.Fatalf("expected 18 screens under test, got %d", len(screens))
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

func gitRepoForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q", "-b", "main", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	return dir
}

func TestTodoSelectionFollowsCard(t *testing.T) {
	zone.NewGlobal()
	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 45})
	p := m.projects[0]
	m.useProject(&p)
	m.openTodos()
	m.selectTodo("t20")
	if m.todoCol != 2 {
		t.Fatalf("t20 should start in DOING, col=%d", m.todoCol)
	}
	td, _, _ := m.mgr.Store().Todo("t20")
	td.Status = store.TodoReview
	m.mgr.Store().SaveTodo(td)
	m.reloadTodos()
	if sel, ok := m.selectedTodo(); !ok || sel.ID != "t20" || m.todoCol != 3 {
		t.Fatalf("selection should follow t20 into REVIEW, got %+v col=%d", sel, m.todoCol)
	}
}
