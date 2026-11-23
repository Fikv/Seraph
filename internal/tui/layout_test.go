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
	m.mcp.ext = []mcp.External{
		{Name: "penpot", Targets: []string{"claude"}, Scope: "user", Server: mcp.Server{Name: "penpot", Transport: "http", URL: "http://localhost:9001/mcp/stream?userToken=SECRETTOKEN"}},
		{Name: "shared-" + strings.Repeat("long", 10), Targets: []string{"claude", "cursor"}, Scope: "user", Server: mcp.Server{Name: "shared", Transport: "stdio", Command: "npx", Args: []string{"-y", "shared"}, Env: map[string]string{"API_KEY": "x"}}},
		{Name: "medusa", Targets: []string{"claude"}, Scope: "local", Dir: "/home/u/projekty/" + strings.Repeat("deep/", 10), Server: mcp.Server{Name: "medusa", Transport: "sse", URL: "https://x/sse"}},
	}
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
	seedMissions(t, st, notes, repo)
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
	m.refreshStats()
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
		"mcp-external":  func(m *Model) { m.scr = scrMCP; m.mcp.tab = 0; m.mcp.sel = 3 },
		"mcp-ext-local": func(m *Model) { m.scr = scrMCP; m.mcp.tab = 0; m.mcp.sel = 5 },
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
		"missions-plan-gate": func(m *Model) { m.openMissions("mplan") },
		"missions-tree-sel":  func(m *Model) { m.openMissions("mplan"); m.missions.nodeSel = 6 },
		"missions-blocked":   func(m *Model) { m.openMissions("mblock") },
		"missions-empty":     func(m *Model) { p := m.projects[0]; m.useProject(&p); m.project.ID = "none"; m.openMissions("") },
		"missions-new": func(m *Model) {
			m.openMissions("")
			m.handleKey(keyMsg("n"))
			m.ta.SetValue(strings.Repeat("goal text ", 40))
		},
		"missions-revise": func(m *Model) {
			m.openMissions("mplan")
			m.handleKey(keyMsg("e"))
			m.missions.input.SetValue(strings.Repeat("change ", 50))
		},
		"missions-roles": func(m *Model) {
			m.mgr.SetRole(store.RoleReviewer, &orch.RoleConfig{Agent: "cursor", Model: "gpt-5.3-codex-high"})
			m.openMissions("")
			m.handleKey(keyMsg("R"))
		},
		"missions-artifact": func(m *Model) { m.openMissions("mplan"); m.handleKey(keyMsg("a")) },
		"release-fail":      func(m *Model) { m.openRelease(store.TargetSession, "s00000", scrBoard) },
		"release-merged":    func(m *Model) { m.openRelease(store.TargetSession, "s00003", scrBoard); m.release.armed = "P" },
		"release-report":    func(m *Model) { m.openRelease(store.TargetSession, "s00000", scrBoard); m.handleKey(keyMsg("a")) },
		"release-mission":   func(m *Model) { m.openMissions("mdone"); m.handleKey(keyMsg("w")) },
		"release-none":      func(m *Model) { m.openRelease(store.TargetSession, "s00002", scrBoard) },
		"permission": func(m *Model) {
			m.scr = scrBoard
			m.stats.permissions = []store.Permission{{ID: "p1", SessionID: "s00000", Tool: "Bash", Input: map[string]any{"command": "git push origin main --force-with-lease " + strings.Repeat("--very-long-flag ", 12), "description": strings.Repeat("Push the branch ", 10)}}, {ID: "p2", SessionID: "s00001", Tool: "WebFetch", Input: map[string]any{"url": "https://example.com"}}}
		},
		"detail-waiting": func(m *Model) {
			m.openDetail("s00000")
			m.stats.waitingFor = map[string]string{"s00000": "Bash: " + strings.Repeat("rm -rf build ", 20)}
			m.permitSnooze["p1"] = time.Now().Add(time.Hour)
		},
		"detail-select": func(m *Model) { m.openDetail("s00000"); m.toggleSelectMode() },
		"detail-chat":   func(m *Model) { m.openDetail("s00000"); m.handleKey(keyMsg("tab")) },
		"todo-form-model": func(m *Model) {
			p := m.projects[0]
			m.useProject(&p)
			m.openTodos()
			m.openTodoForm(nil)
			m.todoForm.agent, m.todoForm.model = "cursor", "grok-4.7-high"
		},
	}
	if len(screens) != 45 {
		t.Fatalf("expected 45 screens under test, got %d", len(screens))
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

func seedMissions(t *testing.T, st *store.Store, notes, repo string) {
	t.Helper()
	art := filepath.Join(notes, "Seraph Missions", "m")
	os.MkdirAll(art, 0o755)
	spec := filepath.Join(art, "spec.md")
	os.WriteFile(spec, []byte("# Spec\n\n## Acceptance criteria\n1. AC1 "+strings.Repeat("long criterion ", 30)+"\n"), 0o644)
	plan := filepath.Join(art, "plan.md")
	os.WriteFile(plan, []byte("# Plan\n"+strings.Repeat("- step\n", 80)), 0o644)
	now := time.Now()
	st.SaveMission(store.Mission{ID: "mplan", ProjectID: "", Title: "Payments with BLIK and a long mission title that keeps going", Goal: "g", Repo: repo, Base: "main",
		Branch: "seraph/m-mplan", Dir: repo, Artifacts: art, Phase: store.PhasePlan, State: store.MissionWaiting, Gate: store.PhasePlan, CreatedAt: now})
	st.SaveMission(store.Mission{ID: "mblock", Title: "Blocked one", Goal: "g", Repo: repo, Base: "main", Branch: "seraph/m-mblock", Dir: repo, Artifacts: art,
		Phase: store.PhaseBuild, State: store.MissionBlocked, Error: "merge conflict in " + strings.Repeat("internal/very/long/path.go, ", 6), CreatedAt: now.Add(-time.Hour)})
	st.SaveMission(store.Mission{ID: "mdone", Title: "Done one", Goal: "g", Repo: repo, Branch: "seraph/m-mdone", Artifacts: art, Phase: store.PhaseDone, State: store.MissionDone, CreatedAt: now.Add(-2 * time.Hour)})
	nodes := []store.Node{
		{ID: "n1", MissionID: "mplan", Role: store.RolePO, Title: "Specification", Status: store.NodeDone, Artifact: spec, SessionID: "s00000"},
		{ID: "n2", MissionID: "mplan", Role: store.RoleAnalyst, Title: strings.Repeat("How does the checkout talk to the payment provider? ", 3), Status: store.NodeDone, SessionID: "s00001"},
		{ID: "n3", MissionID: "mplan", Role: store.RoleAnalyst, Title: "Where are webhooks verified?", Status: store.NodeWorking, SessionID: "s00003"},
		{ID: "n4", MissionID: "mplan", Role: store.RoleLead, Title: "Plan", Status: store.NodeDone, Artifact: plan, Note: strings.Repeat("\"api\" and \"ui\" both own src/** / src/api/** and run in parallel\n", 3)},
		{ID: "n5", MissionID: "mplan", Role: store.RoleDev, Title: "Backend endpoint for BLIK codes", Files: []string{"internal/api/**", "internal/payments/blik/**"}, Status: store.NodeMerged, Round: 1},
		{ID: "n6", MissionID: "mplan", ParentID: "n5", Role: store.RoleReviewer, Title: "Review of Backend endpoint", Status: store.NodeDone, Verdict: "CHANGES"},
		{ID: "n7", MissionID: "mplan", ParentID: "n5", Role: store.RoleReviewer, Title: "Review of Backend endpoint", Status: store.NodeDone, Verdict: "APPROVE", Round: 1},
		{ID: "n8", MissionID: "mplan", Role: store.RoleDev, Title: "UI", Files: []string{"web/**"}, Status: store.NodeFailed, Note: "boom"},
		{ID: "n9", MissionID: "mplan", Role: store.RoleTester, Title: "Tests", Status: store.NodeDone, Verdict: "FAIL"},
		{ID: "n10", MissionID: "mplan", ParentID: "n9", Role: store.RoleDev, Title: "Fix failing tests", Status: store.NodePending},
		{ID: "n11", MissionID: "mplan", Role: store.RoleAcceptance, Title: "Acceptance", Status: store.NodePending},
	}
	for i, n := range nodes {
		n.Position = i + 1
		if err := st.SaveNode(n); err != nil {
			t.Fatal(err)
		}
	}
	st.SaveNode(store.Node{ID: "b1", MissionID: "mblock", Role: store.RoleDev, Title: "conflicting", Status: store.NodeFailed, Note: "merge conflict", Position: 1})
	report := "## Requests\n| push | release step |\n## Problems\n- [blocking] README.md:12 " + strings.Repeat("broken link ", 20) + "\nVERDICT: FAIL"
	st.SaveRelease(store.Release{ID: "r1", Kind: store.TargetSession, TargetID: "s00000", Title: "Add a very long title " + strings.Repeat("x", 80), Repo: repo, Branch: "seraph/s00000", Base: "main", Status: store.ReleaseFail, Report: report, Note: strings.Repeat("the validator changed files; they were stashed ", 3), SessionID: "s00001"})
	st.SaveRelease(store.Release{ID: "r2", Kind: store.TargetSession, TargetID: "s00003", Title: "Another done", Repo: repo, Branch: "seraph/s00003", Base: "main", Status: store.ReleasePass, Report: "VERDICT: PASS", MergedAt: time.Now(), MergeSHA: "abc1234"})
	st.SaveRelease(store.Release{ID: "r3", Kind: store.TargetMission, TargetID: "mdone", Title: "Done one", Repo: repo, Branch: "seraph/m-mdone", Base: "main", Status: store.ReleasePass, Report: "VERDICT: PASS", MergedAt: time.Now(), PushedAt: time.Now()})
}

func TestMissionsScreenFlow(t *testing.T) {
	zone.NewGlobal()
	m := testModel(t)
	m.w, m.h = 140, 44
	m.scr = scrBoard
	m.handleKey(keyMsg("M"))
	if m.scr != scrMissions || len(m.missions.list) != 3 {
		t.Fatalf("missions screen: scr=%v list=%d", m.scr, len(m.missions.list))
	}
	v := ansi.Strip(m.View())
	for _, want := range []string{"Orchestrator", "Product Owner", "Analyst", "Tech Lead", "Developer", "Reviewer", "Fixer", "Tester", "Acceptance", "PLAN READY FOR YOUR DECISION", "both own", "approve", "changes", "fail"} {
		if !strings.Contains(v, want) {
			t.Errorf("missions view lacks %q", want)
		}
	}
	m.handleKey(keyMsg("tab"))
	m.handleKey(keyMsg("a"))
	if m.missions.mode != mmArtifact || !strings.Contains(ansi.Strip(m.View()), "Spec") {
		t.Fatal("a on the PO node must open spec.md")
	}
	m.handleKey(keyMsg("esc"))
	m.handleKey(keyMsg("down"))
	m.handleKey(keyMsg("enter"))
	if m.scr != scrDetail || m.openID != "s00001" {
		t.Fatalf("enter on a node opens its worker: scr=%v id=%s", m.scr, m.openID)
	}
	m.handleKey(keyMsg("esc"))
	if m.scr != scrMissions {
		t.Fatal("esc from a worker returns to missions")
	}
	m.handleKey(keyMsg("R"))
	m.handleKey(keyMsg("enter"))
	if m.scr != scrModels {
		t.Fatal("roles: enter opens the model picker")
	}
	m.pickModel(agent.Model{Agent: "cursor", ID: "gemini-3.1-pro", Name: "Gemini 3.1 Pro", Provider: "Google"})
	if c := m.mgr.Role(store.RolePO); c.Agent != "cursor" || c.Model != "gemini-3.1-pro" || c.Approval != agent.ApprovalReadOnly {
		t.Fatalf("role = %+v", c)
	}
	if m.scr != scrMissions || m.missions.mode != mmRoles {
		t.Fatal("picker must return to the roles list")
	}
	m.handleKey(keyMsg("esc"))
	m.handleKey(keyMsg("esc"))
	if m.scr != scrBoard {
		t.Fatal("esc returns to the board")
	}
}

func TestMouseGarbageFilter(t *testing.T) {
	m := testModel(t)
	runes := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
	for _, junk := range []tea.KeyMsg{
		runes("[<35;77;27M"), runes("<35;60;12M"), runes("[<35;60"), runes("35;60;12M"),
		{Type: tea.KeyRunes, Runes: []rune("["), Alt: true},
	} {
		if !m.mouseGarbage(junk) {
			t.Errorf("%q must be dropped", junk.String())
		}
	}
	if !m.mouseGarbage(runes("M")) {
		t.Error("a lone tail right after a fragment must be dropped")
	}
	m.swallowAt = time.Time{}
	for _, ok := range []tea.KeyMsg{runes(";"), runes("5"), runes("M"), runes("a"), runes("<"), {Type: tea.KeyRunes, Runes: []rune("[<35;1;1M stays"), Paste: true}, {Type: tea.KeyEnter}} {
		if m.mouseGarbage(ok) {
			t.Errorf("%q is real input and must pass", ok.String())
		}
	}
}

func TestIdleMotionSkipsRender(t *testing.T) {
	zone.NewGlobal()
	m := testModel(t)
	m.w, m.h, m.scr = 120, 40, scrBoard
	m.ta.Focus()
	first := m.View()
	m.handleMouse(tea.MouseMsg{X: 200, Y: 200, Action: tea.MouseActionMotion})
	if !m.skipRender || m.View() != first {
		t.Fatal("motion that changes nothing must reuse the last frame")
	}
	if m.skipRender {
		t.Fatal("the skip flag must reset after one frame")
	}
	m.scr = scrNew
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<35;77;27M")})
	if strings.Contains(m.ta.Value(), "35;77") {
		t.Fatalf("mouse garbage leaked into the input: %q", m.ta.Value())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if m.ta.Value() != "x" {
		t.Fatalf("real typing must reach the input: %q", m.ta.Value())
	}
}

func TestReleaseScreen(t *testing.T) {
	zone.NewGlobal()
	m := testModel(t)
	m.w, m.h, m.scr = 140, 44, scrBoard
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "validation failed") || !strings.Contains(v, "merged") {
		t.Fatalf("cards must show release state:\n%s", v)
	}
	m.lane = laneAscended
	for i, x := range m.lanes()[laneAscended] {
		if x.ID == "s00000" {
			m.idx[laneAscended] = i
		}
	}
	m.handleKey(keyMsg("w"))
	if m.scr != scrRelease || m.release.id != "s00000" {
		t.Fatalf("w opens the release of the selected worker: %v", m.scr)
	}
	v = ansi.Strip(m.View())
	for _, want := range []string{"1 VALIDATE", "FAIL", "[blocking] README.md:12", "2 MERGE", "3 PUSH", "send findings", "stashed"} {
		if !strings.Contains(v, want) {
			t.Errorf("release view lacks %q", want)
		}
	}
	if strings.Contains(v, " m  merge") {
		t.Error("a failed validation must not offer merge")
	}
	m.handleKey(keyMsg("a"))
	if !m.release.report {
		t.Fatal("a opens the report")
	}
	m.handleKey(keyMsg("esc"))
	m.handleKey(keyMsg("esc"))
	if m.scr != scrBoard || m.release != nil {
		t.Fatal("esc closes the release")
	}
	for _, s := range m.mgr.Sessions() {
		if s.Role != "" {
			t.Fatal("fixture should not contain validator sessions")
		}
	}
}

func TestReleaseSummaryLine(t *testing.T) {
	if got := summaryLine("No [blocking] problems found.\nVERDICT: PASS"); got != "no blocking problems reported" {
		t.Fatalf("prose mentioning [blocking] is not a finding: %q", got)
	}
	if got := summaryLine("## Problems\n- [blocking] a.go:1 x\n2. [blocking] b.go:2 y\n- [minor] c"); !strings.HasPrefix(got, "2 blocking problems · first: [blocking] a.go:1") {
		t.Fatalf("summary = %q", got)
	}
	if agoText(time.Now()) != "just now" {
		t.Fatal("agoText")
	}
}

func TestWorkingBarBounces(t *testing.T) {
	w := 60
	seen := map[int]bool{}
	prev := -1
	dir, flips := 0, 0
	for ms := 0; ms < 4000; ms += 20 {
		bar := ansi.Strip(workingBar(w, time.Duration(ms)*time.Millisecond))
		if n := len([]rune(bar)); n != w {
			t.Fatalf("bar width %d != %d", n, w)
		}
		pos := strings.IndexRune(bar, '▰') / len("▰")
		pos = len([]rune(bar[:strings.IndexRune(bar, '▰')]))
		seen[pos] = true
		if prev >= 0 && pos != prev {
			d := 1
			if pos < prev {
				d = -1
			}
			if dir != 0 && d != dir {
				flips++
			}
			dir = d
		}
		prev = pos
	}
	if !seen[0] || !seen[w-barSeg] || flips < 2 {
		t.Fatalf("the bar must reach both ends and turn back: ends=%v/%v flips=%d", seen[0], seen[w-barSeg], flips)
	}
}

func TestPermissionModal(t *testing.T) {
	zone.NewGlobal()
	m := testModel(t)
	m.w, m.h, m.scr = 120, 40, scrBoard
	m.stats.permissions = []store.Permission{{ID: "px", SessionID: "s00000", Tool: "Bash", Input: map[string]any{"command": "git push origin main"}}}
	v := ansi.Strip(m.View())
	for _, want := range []string{"PERMISSION", "git push origin main", "allow once", "always allow `git push …`", "deny"} {
		if !strings.Contains(v, want) {
			t.Errorf("modal lacks %q", want)
		}
	}
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if m.scr != scrBoard {
		t.Fatal("keys must go to the modal, not the board (n would open a new worker)")
	}
	m.stats.permissions = []store.Permission{{ID: "py", SessionID: "s00000", Tool: "Bash", Input: map[string]any{"command": "rm -rf x"}}}
	m.permAt = time.Now()
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if _, ok := m.currentPermission(); ok {
		t.Fatal("esc snoozes the request")
	}
}

func TestChatViewHidesProcess(t *testing.T) {
	evs := []agent.Event{
		{Kind: agent.KindUser, Text: "write the README"},
		{Kind: agent.KindInit},
		{Kind: agent.KindThinking, Text: "SECRET-THOUGHT"},
		{Kind: agent.KindText, Text: "NARRATION I will read files first"},
		{Kind: agent.KindToolStart, Tool: "Bash", Text: "TOOLCALL cat package.json"},
		{Kind: agent.KindToolEnd},
		{Kind: agent.KindText, Text: "FINAL README is ready"},
		{Kind: agent.KindResult, Text: "FINAL README is ready", Cost: 0.01},
		{Kind: agent.KindSystem, Text: "committed abc on seraph/x"},
		{Kind: agent.KindUser, Text: "now push it"},
		{Kind: agent.KindToolStart, Tool: "Bash", Text: "git push"},
		{Kind: agent.KindError, Text: "BOOM permission denied"},
	}
	out := ansi.Strip(renderChat(evs, "claude", 100, false))
	for _, want := range []string{"write the README", "FINAL README is ready", "now push it", "BOOM permission denied", "steps hidden"} {
		if !strings.Contains(out, want) {
			t.Errorf("chat view lacks %q:\n%s", want, out)
		}
	}
	for _, hidden := range []string{"SECRET-THOUGHT", "NARRATION", "TOOLCALL", "committed abc"} {
		if strings.Contains(out, hidden) {
			t.Errorf("chat view must hide %q", hidden)
		}
	}
	if strings.Count(out, "FINAL README is ready") != 1 {
		t.Error("the final answer must appear once")
	}
	running := ansi.Strip(renderChat(evs[:5], "claude", 100, true))
	if !strings.Contains(running, "working…") || strings.Contains(running, "NARRATION") {
		t.Errorf("a running turn shows a placeholder, not narration:\n%s", running)
	}
	if stopped := ansi.Strip(renderChat(evs[:5], "claude", 100, false)); !strings.Contains(stopped, "NARRATION") {
		t.Error("a turn that ended without a result shows the last thing the agent said")
	}
	full := ansi.Strip(renderTranscript(evs, "claude", 100))
	if !strings.Contains(full, "SECRET-THOUGHT") || !strings.Contains(full, "TOOLCALL") {
		t.Error("the full view keeps everything")
	}
}

func TestChatViewToggleSticks(t *testing.T) {
	m := testModel(t)
	m.w, m.h = 120, 40
	m.openDetail("s00000")
	m.handleKey(keyMsg("tab"))
	if !m.chatView || m.mgr.Store().Setting(TranscriptViewSetting) != "chat" {
		t.Fatal("tab switches to the conversation view and remembers it")
	}
	if strings.Contains(ansi.Strip(m.vp.View()), "⟢ Bash") {
		t.Fatal("tool calls must be hidden")
	}
	m2 := newModel(m.mgr, m.opt)
	if !m2.chatView {
		t.Fatal("the choice must survive a restart")
	}
	m.handleKey(keyMsg("tab"))
	m.vp.GotoTop()
	if m.chatView || m.mgr.Store().Setting(TranscriptViewSetting) != "full" || !strings.Contains(ansi.Strip(m.vp.View()), "Bash") {
		t.Fatal("tab brings the full transcript back")
	}
}

func TestMCPExternalRows(t *testing.T) {
	zone.NewGlobal()
	m := testModel(t)
	m.w, m.h, m.scr = 150, 44, scrMCP
	m.mcp.tab, m.mcp.sel = 0, 0
	for i := 0; i < 3; i++ {
		m.handleKey(keyMsg("down"))
	}
	v := ansi.Strip(m.View())
	for _, want := range []string{"ADDED OUTSIDE SERAPH · 3", "penpot", "Claude Code (user scope)", "userToken=••••", "import into Seraph"} {
		if !strings.Contains(v, want) {
			t.Errorf("mcp view lacks %q", want)
		}
	}
	if strings.Contains(v, "SECRETTOKEN") {
		t.Fatal("tokens must never be shown")
	}
	m.mcp.sel = 5
	if v := ansi.Strip(m.View()); !strings.Contains(v, "do not see it") {
		t.Error("local-scope servers must warn that workers miss them")
	}
}

func TestTrustAndWorkerMode(t *testing.T) {
	zone.NewGlobal()
	m := testModel(t)
	m.w, m.h, m.scr = 150, 44, scrMCP
	m.mcp.tab, m.mcp.sel = 0, 3
	m.handleKey(keyMsg("a"))
	if !mcp.IsTrusted(m.mgr.Store(), "penpot") {
		t.Fatal("a trusts the selected external server")
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "penpot ⚡") || !strings.Contains(v, "mcp__penpot") {
		t.Fatalf("trusted servers are marked:\n%s", v)
	}
	m.handleKey(keyMsg("a"))
	if mcp.IsTrusted(m.mgr.Store(), "penpot") {
		t.Fatal("a again untrusts")
	}
	m.scr = scrBoard
	m.openDetail("s00000")
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlY})
	if s, _ := m.mgr.Get("s00000"); s.Approval != "confirm" {
		t.Fatalf("ctrl+y switches the worker to confirm, got %s", s.Approval)
	}
	m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlY})
	if s, _ := m.mgr.Get("s00000"); s.Approval != "safe" {
		t.Fatalf("a shared-dir worker skips yolo, got %s", s.Approval)
	}
	m.stats.permissions = []store.Permission{{ID: "pm", SessionID: "s00000", Tool: "mcp__penpot__execute_code", Input: map[string]any{"code": "penpot.createRectangle()"}}}
	v := ansi.Strip(m.View())
	for _, want := range []string{"MCP penpot · execute_code", "penpot.createRectangle()", "c → a"} {
		if !strings.Contains(v, want) {
			t.Errorf("MCP permission modal lacks %q", want)
		}
	}
}
