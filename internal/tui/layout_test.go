package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	"seraph/internal/workspace"
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
	m.cursorModels = agent.ParseCursorModels("auto - Auto (default)\ngpt-5.3-codex-high - Codex 5.3 High\nclaude-opus-5-5-high - Claude Opus 5.5 1M High\ngemini-3.1-pro - Gemini 3.1 Pro\ngrok-4.7-high - Grok 4.7 High\nmuse-spark-1.3 - Muse Spark 1.3\nkimi-k3 - Kimi K3\nglm-5.2 - GLM 5.2\n" + strings.Repeat("x", 120) + " - " + strings.Repeat("Very long model name ", 8) + "\n")
	st.SetSetting("limits:claude", `{"agent":"claude","windows":[{"window":"five_hour","utilization":0.62,"resets_at":"`+time.Now().Add(3*time.Hour).Format(time.RFC3339)+`"},{"window":"seven_day","utilization":0.91,"resets_at":"`+time.Now().Add(80*time.Hour).Format(time.RFC3339)+`"}],"captured":"`+time.Now().Format(time.RFC3339)+`"}`)

	wt, branch, err := workspace.Create(t.TempDir(), repo, "dfx001")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(wt, "main.go"), []byte("package main\n\nfunc main() {\n\tprintln(\""+strings.Repeat("very long line ", 20)+"\")\n}\n"), 0o644)
	os.MkdirAll(filepath.Join(wt, strings.Repeat("deep/", 8)), 0o755)
	os.WriteFile(filepath.Join(wt, strings.Repeat("deep/", 8)+"x.md"), []byte("# deep\n"), 0o644)
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	if _, err := workspace.CommitAll(wt, "worker change"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(wt, "untracked.txt"), []byte("x"), 0o644)
	st.Save(store.Session{ID: "dfx001", Title: "diff session " + strings.Repeat("title ", 10), Task: "t", Agent: "cursor", Status: store.StatusDone,
		Repo: repo, Dir: wt, Branch: branch, Base: "main", Worktree: true, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	os.WriteFile(filepath.Join(notes, "Links.md"), []byte("---\ntags: [a, b]\n---\n# Links\n\nSee [[Architecture]] and [[Missing|alias]].\n\n```go\nfunc x() {}\n```\n"+strings.Repeat("long paragraph text ", 60)), 0o644)
	m.mgr.Refresh()
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
		"board-empty":   func(m *Model) { p := m.projects[0]; m.useProject(&p); m.scr = scrBoard; m.project.ID = "nothing-here" },
		"notes": func(m *Model) {
			p := m.projects[0]
			m.useProject(&p)
			m.openNotesBrowser()
			m.followLink("Links")
		},
		"notes-links": func(m *Model) {
			p := m.projects[0]
			m.useProject(&p)
			m.openNotesBrowser()
			m.followLink("Links")
			m.notes.linkPick = true
		},
		"diff":     func(m *Model) { m.openDiff("dfx001", scrBoard); m.diff.focus = 1 },
		"diff-ask": func(m *Model) { m.openDiff("dfx001", scrBoard); m.startAsk(false) },
		"models":   func(m *Model) { m.openModelPicker(scrBoard, m.setDefaultModel); m.picker.sel = 9 },
		"models-filter": func(m *Model) {
			m.openModelPicker(scrBoard, m.setDefaultModel)
			m.picker.filter.SetValue("zzz")
			m.applyModelFilter()
			m.catalogErr = strings.Repeat("boom ", 30)
		},
		"todo-form-model": func(m *Model) {
			p := m.projects[0]
			m.useProject(&p)
			m.openTodos()
			m.openTodoForm(nil)
			m.todoForm.agent, m.todoForm.model = "cursor", "grok-4.7-high"
		},
	}
	if len(screens) != 26 {
		t.Fatalf("expected 26 screens under test, got %d", len(screens))
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
	for _, args := range [][]string{{"init", "-q", "-b", "main", dir}, {"-C", dir, "commit", "-q", "--allow-empty", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
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

func TestNotesAndDiffContent(t *testing.T) {
	zone.NewGlobal()
	m := testModel(t)
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 45})
	p := m.projects[0]
	m.useProject(&p)
	m.openNotesBrowser()
	m.followLink("Links")
	if f, _ := m.currentNote(); filepath.Base(f.Rel) != "Links" {
		t.Fatalf("followLink opened %q", f.Rel)
	}
	if !reflect.DeepEqual(m.notes.links, []string{"Architecture", "Missing"}) || !reflect.DeepEqual(m.notes.tags, []string{"a", "b"}) {
		t.Fatalf("links=%v tags=%v", m.notes.links, m.notes.tags)
	}
	m.followLink("Architecture")
	if f, _ := m.currentNote(); filepath.Base(f.Rel) != "Architecture" {
		t.Fatalf("wikilink not followed: %q", f.Rel)
	}
	if out := ansi.Strip(m.View()); !strings.Contains(out, "Architecture") {
		t.Fatal("preview missing")
	}

	m.openDiff("dfx001", scrBoard)
	if len(m.diff.res.Files) != 2 || len(m.diff.res.Commits) != 1 || len(m.diff.res.Untracked) != 1 || m.diff.err != "" {
		t.Fatalf("diff = %+v err=%s", m.diff.res, m.diff.err)
	}
	for i, f := range m.diff.res.Files {
		if f.Path == "main.go" {
			m.diff.fileSel = i
		}
	}
	out := ansi.Strip(m.View())
	for _, want := range []string{"worker change", "main.go", "+package main", "1 untracked"} {
		if !strings.Contains(out, want) {
			t.Errorf("diff view missing %q", want)
		}
	}
}

func TestSpinStarCycles(t *testing.T) {
	seen := map[string]bool{}
	for f := 0; f < len(spinFrames)*spinEvery; f++ {
		seen[spinStar(f, false)] = true
		if w := ansi.StringWidth(spinStar(f, true)); w != 1 {
			t.Fatalf("frame %d is %d cells wide", f, w)
		}
	}
	if len(seen) != 3 || spinStar(0, false) != "✦" || spinStar(2*spinEvery, false) != "✕" {
		t.Fatalf("frames = %v", seen)
	}
	if spinStar(spinEvery, false) != spinStar(3*spinEvery, true) {
		t.Fatal("right star should run the sequence backwards")
	}
}

func TestEmblem(t *testing.T) {
	built := emblem(3 * time.Second)
	if len(built) != emblemH {
		t.Fatalf("rows = %d", len(built))
	}
	for i, r := range built {
		if w := ansi.StringWidth(r); w != emblemW {
			t.Fatalf("row %d is %d wide, want %d", i, w, emblemW)
		}
	}
	plain := ansi.Strip(strings.Join(built, "\n"))
	if !strings.Contains(plain, "┏━╸ ┏━╸ ┏━┓ ┏━┓ ┏━┓ ╻ ╻") || !strings.Contains(plain, "─") {
		t.Fatalf("emblem word or orbit missing:\n%s", plain)
	}
	if early := ansi.Strip(strings.Join(emblem(-time.Second), "")); strings.TrimSpace(early) != "" {
		t.Fatalf("before the build everything should be blank: %q", early)
	}
	stars := 0
	for _, ms := range []int{1500, 2500, 3500, 4500, 5500} {
		p := ansi.Strip(strings.Join(emblem(time.Duration(ms)*time.Millisecond), ""))
		if strings.ContainsAny(p, "✦✶✕") {
			stars++
		}
	}
	if stars < 3 {
		t.Fatalf("the star should be visible most of the lap, seen in %d/5 frames", stars)
	}

	m := testModel(t)
	m.logoStyle = LogoOrbit
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 45})
	m.scr = scrBoard
	m.emblemStart = time.Now().Add(-5 * time.Second)
	if out := ansi.Strip(m.View()); !strings.Contains(out, "┏━╸ ┏━╸") || !strings.Contains(out, "ORCHESTRATE THE CHOIR") {
		t.Fatal("board should show the corner emblem on a large terminal")
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if out := ansi.Strip(m.View()); strings.Contains(out, "┏━╸ ┏━╸") || !strings.Contains(out, "SERAPH") {
		t.Fatal("small terminals should use the compact header")
	}
}

func TestLineMark(t *testing.T) {
	built := lineMark(3 * time.Second)
	if len(built) != lineH {
		t.Fatalf("rows = %d", len(built))
	}
	for i, r := range built {
		if w := ansi.StringWidth(r); w != lineW {
			t.Fatalf("row %d is %d wide, want %d", i, w, lineW)
		}
	}
	plain := ansi.Strip(strings.Join(built, "\n"))
	if !strings.Contains(plain, "╭──╴ ╭──╴ ╭──╮ ╭──╮ ╭──╮ ╷  ╷") || !strings.Contains(plain, "╶") {
		t.Fatalf("line mark:\n%s", plain)
	}
	if strings.TrimSpace(ansi.Strip(strings.Join(lineMark(-time.Second), ""))) != "" {
		t.Fatal("before the build everything should be blank")
	}
	if logoStyleFor("") != LogoOrbit || logoStyleFor(LogoLine) != LogoLine {
		t.Fatal("default must be orbit, setting line selects the alternative")
	}
	t.Setenv("SERAPH_LOGO", LogoLine)
	if logoStyleFor("") != LogoLine {
		t.Fatal("SERAPH_LOGO must override the setting")
	}

	m := testModel(t)
	m.logoStyle = LogoLine
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 45})
	m.scr = scrBoard
	m.emblemStart = time.Now().Add(-5 * time.Second)
	out := ansi.Strip(m.View())
	if !strings.Contains(out, "╭──╴ ╭──╴ ╭──╮") || !strings.Contains(out, "ORCHESTRATE THE CHOIR") || !strings.Contains(out, "spent") {
		t.Fatal("board should show the line logo with the poster info")
	}
}

func TestModelPickerFlow(t *testing.T) {
	zone.NewGlobal()
	m := testModel(t)
	m.w, m.h = 120, 40
	m.openModelPicker(scrBoard, m.setDefaultModel)
	for _, r := range "grok" {
		m.modelsKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if len(m.picker.view) != 1 {
		t.Fatalf("filter grok -> %d rows", len(m.picker.view))
	}
	m.modelsKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.scr != scrBoard || m.opt.Agent != "cursor" || m.opt.Model != "grok-4.7-high" {
		t.Fatalf("picked %s/%s scr=%v", m.opt.Agent, m.opt.Model, m.scr)
	}
	if m.mgr.Store().Setting(DefaultModelSetting) != "grok-4.7-high" || m.mgr.Store().Setting(DefaultAgentSetting) != "cursor" {
		t.Fatal("default model not persisted")
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Grok 4.7 High") || !strings.Contains(v, "xAI") {
		t.Fatalf("board header lacks model/provider:\n%s", v)
	}
	m.openModelPicker(scrBoard, m.setDefaultModel)
	v = ansi.Strip(m.View())
	for _, want := range []string{"Anthropic", "OpenAI", "Google", "Meta", "Moonshot AI", "5h", "62%", "7d", "91%"} {
		if !strings.Contains(v, want) {
			t.Errorf("picker lacks %q", want)
		}
	}
}
