package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"seraph/internal/agent"
	"seraph/internal/orch"
	"seraph/internal/store"
	"seraph/internal/workspace"
)

type screen int

const (
	scrSplash screen = iota
	scrBoard
	scrDetail
	scrNew
	scrPlanGoal
	scrPlanning
	scrPlanReview
	scrConfirm
	scrMenu
	scrMCP
	scrProjects
	scrTodos
	scrNotes
	scrDiff
)

const continuePrompt = "Continue where you left off and finish the task."

type tickMsg time.Time

type versionsMsg map[string]string

type busMsg string

type planDoneMsg struct {
	tasks []orch.PlannedTask
	err   error
}

type Options struct {
	Repo       string
	Dir        string
	Agent      string
	Model      string
	Approval   agent.Approval
	SkipSplash bool
	Version    string
	Storage    string
	Updates    <-chan string
}

type Model struct {
	mgr *orch.Manager
	opt Options

	w, h        int
	scr         screen
	frame       int
	splashStart time.Time
	splashDone  bool
	emblemStart time.Time
	lastRefresh time.Time
	versions    map[string]string

	lane lane
	idx  [laneCount]int

	openID    string
	vp        viewport.Model
	vpVersion int
	vpWidth   int
	wsSummary string
	wsAt      time.Time

	ta textarea.Model
	fi textinput.Model

	plan       []orch.PlannedTask
	planSel    int
	planGoal   string
	planCancel context.CancelFunc
	planStart  time.Time

	confirmID string
	quitArmed bool

	flash    string
	flashErr bool
	flashAt  time.Time

	repoText string

	mcp *mcpState

	project      *store.Project
	projects     []store.Project
	projSel      int
	projForm     *projectForm
	projRemoving string
	todos        []store.Todo
	todosAt      time.Time
	todoCol      int
	todoIdx      [5]int
	todoForm     *todoForm
	todoRemoving string
	todoSelID    string
	notesCache   map[string]notesEntry
	backTo       screen
	notes        *notesState
	diff         *diffState

	hover       string
	zoneIDs     []string
	lastClick   string
	lastClickAt time.Time
}

func Run(mgr *orch.Manager, opt Options) error {
	m := newModel(mgr, opt)
	zone.NewGlobal()
	defer zone.Close()
	mgr.StartScheduler(m.defaults())
	_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion()).Run()
	return err
}

func newModel(mgr *orch.Manager, opt Options) *Model {
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.CharLimit = 16000
	ta.SetHeight(6)
	ta.Prompt = "┃ "
	ta.FocusedStyle.Prompt = sGold
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Text = sIvory
	ta.FocusedStyle.Placeholder = sFaint
	ta.BlurredStyle = ta.FocusedStyle
	ta.KeyMap.InsertNewline.SetKeys("alt+enter", "ctrl+j")

	fi := textinput.New()
	fi.Prompt = "⟡ › "
	fi.PromptStyle = sGold
	fi.TextStyle = sIvory
	fi.PlaceholderStyle = sFaint
	fi.Placeholder = "follow-up message · enter to send"
	fi.CharLimit = 8000

	m := &Model{mgr: mgr, opt: opt, ta: ta, fi: fi, vp: viewport.New(80, 20)}
	m.loadCurrentProject()
	m.repoText = m.repoLabel()
	if opt.SkipSplash {
		m.scr = scrBoard
	} else {
		m.scr = scrSplash
		m.splashStart = time.Now()
	}
	ls := m.lanes()
	for l := lane(0); l < laneCount; l++ {
		if len(ls[l]) > 0 {
			m.lane = l
			break
		}
	}
	return m
}

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) tickEvery() time.Duration {
	if m.scr == scrSplash {
		return 33 * time.Millisecond
	}
	if m.scr == scrBoard && m.cornerEmblem() {
		return 66 * time.Millisecond
	}
	return 110 * time.Millisecond
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(tick(m.tickEvery()), m.probeVersions(), waitBus(m.opt.Updates))
}

func waitBus(ch <-chan string) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		id, ok := <-ch
		if !ok {
			return nil
		}
		return busMsg(id)
	}
}

type versionCache interface {
	CacheGet(ctx context.Context, key string) (string, bool)
	CacheSet(ctx context.Context, key, value string, ttl time.Duration)
}

func (m *Model) cache() versionCache {
	if b := m.mgr.Bus(); b != nil {
		return b
	}
	return m.mgr.Store()
}

func (m *Model) probeVersions() tea.Cmd {
	c := m.cache()
	return func() tea.Msg {
		out := versionsMsg{}
		for _, name := range agent.Names() {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			key := "agent:version:" + name
			if v, ok := c.CacheGet(ctx, key); ok {
				out[name] = v
				cancel()
				continue
			}
			d, _ := agent.Get(name)
			v, err := agent.Version(ctx, d)
			if err == nil {
				c.CacheSet(ctx, key, v, time.Hour)
			}
			cancel()
			out[name] = v
		}
		return out
	}
}

func (m *Model) setFlash(msg string, isErr bool) {
	m.flash, m.flashErr, m.flashAt = msg, isErr, time.Now()
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tickMsg:
		m.frame++
		every := time.Second
		if m.opt.Updates != nil {
			every = 5 * time.Second
		}
		if time.Since(m.lastRefresh) > every {
			m.mgr.Refresh()
			m.mgr.SetDefaults(m.defaults())
			m.lastRefresh = time.Now()
		}
		if m.scr == scrDetail {
			m.refreshDetail(false)
		}
		return m, tick(m.tickEvery())
	case mcpSearchMsg, mcpDoneMsg:
		return m.mcpMsg(msg)
	case busMsg:
		m.mgr.Refresh()
		m.lastRefresh = time.Now()
		return m, waitBus(m.opt.Updates)
	case versionsMsg:
		m.versions = msg
		return m, nil
	case planDoneMsg:
		if m.scr != scrPlanning {
			return m, nil
		}
		m.planCancel = nil
		if msg.err != nil {
			m.scr = scrBoard
			m.setFlash("planner failed: "+msg.err.Error(), true)
			return m, nil
		}
		m.plan, m.planSel, m.scr = msg.tasks, 0, scrPlanReview
		return m, nil
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			if m.planCancel != nil {
				m.planCancel()
			}
			return m, tea.Quit
		}
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) layout() {
	mw := m.modalWidth()
	m.ta.SetWidth(mw - 4)
	m.vp.Width = max(m.w-2, 10)
	m.vp.Height = max(m.h-10, 3)
	m.fi.Width = max(m.w-8, 10)
}

func (m *Model) modalWidth() int { return min(max(m.w-6, 30), 96) }

func (m *Model) lanes() [laneCount][]store.Session {
	var out [laneCount][]store.Session
	for _, s := range m.mgr.Sessions() {
		if m.project != nil && s.ProjectID != m.project.ID {
			continue
		}
		l := laneOf(s)
		out[l] = append(out[l], s)
	}
	for l := range out {
		if m.idx[l] >= len(out[l]) {
			m.idx[l] = max(len(out[l])-1, 0)
		}
	}
	return out
}

func (m *Model) selected() (store.Session, bool) {
	ls := m.lanes()
	col := ls[m.lane]
	if len(col) == 0 {
		return store.Session{}, false
	}
	return col[m.idx[m.lane]], true
}

func (m *Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := k.String()
	if key != "q" {
		m.quitArmed = false
	}
	switch m.scr {
	case scrSplash:
		if !m.splashDone {
			m.splashDone = true
			return m, nil
		}
		m.scr = scrBoard
		m.emblemStart = time.Now()
		return m, nil
	case scrBoard:
		return m.boardKey(key)
	case scrDetail:
		return m.detailKey(k)
	case scrNew, scrPlanGoal:
		return m.composeKey(k)
	case scrPlanning:
		if key == "esc" {
			if m.planCancel != nil {
				m.planCancel()
			}
			m.scr = scrBoard
			m.setFlash("planning cancelled", false)
		}
		return m, nil
	case scrPlanReview:
		return m.reviewKey(key)
	case scrMenu:
		return m.menuKey(key)
	case scrMCP:
		return m.mcpKey(k)
	case scrProjects:
		return m.projectsKey(k)
	case scrTodos:
		return m.todosKey(k)
	case scrNotes:
		return m.notesKey(k)
	case scrDiff:
		return m.diffKey(k)
	case scrConfirm:
		switch key {
		case "y", "Y":
			id := m.confirmID
			if s, ok := m.mgr.Get(id); ok {
				if err := m.mgr.Remove(id); err != nil {
					m.setFlash("remove failed: "+err.Error(), true)
				} else if s.Branch != "" {
					m.setFlash("released "+id+" · branch "+s.Branch+" kept", false)
				} else {
					m.setFlash("released "+id, false)
				}
			}
			m.scr = scrBoard
		case "n", "N", "esc", "q":
			m.scr = scrBoard
		}
		return m, nil
	}
	return m, nil
}

func (m *Model) boardKey(key string) (tea.Model, tea.Cmd) {
	ls := m.lanes()
	switch key {
	case "q":
		running := m.mgr.LocalRunning()
		if running > 0 && !m.quitArmed {
			m.quitArmed = true
			m.setFlash(fmt.Sprintf("%d worker(s) still ascending: press q again to stop them and quit", running), true)
			return m, nil
		}
		return m, tea.Quit
	case "left", "h":
		m.lane = (m.lane + laneCount - 1) % laneCount
	case "right", "l", "tab":
		m.lane = (m.lane + 1) % laneCount
	case "up", "k":
		if m.idx[m.lane] > 0 {
			m.idx[m.lane]--
		}
	case "down", "j":
		if m.idx[m.lane] < len(ls[m.lane])-1 {
			m.idx[m.lane]++
		}
	case "enter":
		if s, ok := m.selected(); ok {
			m.openDetail(s.ID)
			return m, textinput.Blink
		}
	case "n":
		m.scr = scrNew
		m.ta.Reset()
		m.ta.Placeholder = "describe the task for your worker…"
		return m, m.ta.Focus()
	case "g":
		m.openProjects()
		return m, nil
	case "t":
		return m, m.openTodos()
	case "p":
		if m.spawnRepo() == "" {
			m.setFlash("planning needs a git repository: run seraph inside one", true)
			return m, nil
		}
		m.scr = scrPlanGoal
		m.ta.Reset()
		m.ta.Placeholder = "describe the goal; the planner splits it into parallel workers…"
		return m, m.ta.Focus()
	case "m":
		m.scr = scrMenu
	case "o":
		return m, m.openNotesBrowser()
	case "v":
		if s, ok := m.selected(); ok {
			return m, m.openDiff(s.ID, scrBoard)
		}
	case "c":
		return m, m.openMCP()
	case "s":
		m.restartSplash()
	case "a":
		m.opt.Agent = agent.Next(m.opt.Agent)
	case "y":
		m.toggleApproval()
	case "x":
		if s, ok := m.selected(); ok && m.mgr.Active(s.ID) {
			m.mgr.Stop(s.ID)
			m.setFlash("stopping "+s.ID, false)
		}
	case "r":
		if s, ok := m.selected(); ok {
			if err := m.mgr.Followup(s.ID, continuePrompt); err != nil {
				m.setFlash(err.Error(), true)
			} else {
				m.setFlash("resumed "+s.ID, false)
			}
		}
	case "d":
		if s, ok := m.selected(); ok {
			m.confirmID = s.ID
			m.scr = scrConfirm
		}
	}
	return m, nil
}

func (m *Model) toggleApproval() {
	if m.opt.Approval == agent.ApprovalYolo {
		m.opt.Approval = agent.ApprovalSafe
	} else {
		m.opt.Approval = agent.ApprovalYolo
	}
}

func (m *Model) openDetail(id string) {
	m.openID = id
	m.scr = scrDetail
	m.vpVersion = -1
	m.wsAt = time.Time{}
	m.fi.SetValue("")
	m.fi.Focus()
	m.refreshDetail(true)
}

func (m *Model) refreshDetail(force bool) {
	s, ok := m.mgr.Get(m.openID)
	if !ok {
		m.scr = scrBoard
		return
	}
	evs, ver := m.mgr.Log(m.openID)
	changed := force || ver != m.vpVersion || m.vpWidth != m.vp.Width
	if changed {
		atBottom := m.vp.AtBottom() || force
		m.vp.SetContent(renderTranscript(evs, s.Agent, m.vp.Width))
		if atBottom {
			m.vp.GotoBottom()
		}
		m.vpVersion, m.vpWidth = ver, m.vp.Width
	}
	if s.Worktree && (force || (changed && time.Since(m.wsAt) > 2*time.Second)) {
		m.wsSummary = workspace.Summary(s.Dir, s.Base)
		m.wsAt = time.Now()
	}
}

func (m *Model) detailKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		if m.fi.Value() != "" {
			m.fi.SetValue("")
			return m, nil
		}
		m.fi.Blur()
		m.scr = scrBoard
		switch m.backTo {
		case scrTodos:
			m.scr = scrTodos
			m.reloadTodos()
		case scrDiff:
			m.scr = scrDiff
			m.reloadDiff()
		}
		m.backTo = scrBoard
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.fi.Value())
		if text == "" {
			return m, nil
		}
		if err := m.mgr.Followup(m.openID, text); err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		m.fi.SetValue("")
		m.refreshDetail(true)
		return m, nil
	case "ctrl+x":
		m.mgr.Stop(m.openID)
		m.setFlash("stopping "+m.openID, false)
		return m, nil
	case "ctrl+g":
		m.fi.Blur()
		return m, m.openDiff(m.openID, scrDetail)
	case "ctrl+r":
		if err := m.mgr.Followup(m.openID, continuePrompt); err != nil {
			m.setFlash(err.Error(), true)
		}
		return m, nil
	case "up", "down", "pgup", "pgdown", "ctrl+u", "ctrl+d":
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(k)
		return m, cmd
	case "home":
		m.vp.GotoTop()
		return m, nil
	case "end":
		m.vp.GotoBottom()
		return m, nil
	}
	var cmd tea.Cmd
	m.fi, cmd = m.fi.Update(k)
	return m, cmd
}

func (m *Model) composeKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.ta.Blur()
		m.scr = scrBoard
		return m, nil
	case "tab":
		m.opt.Agent = agent.Next(m.opt.Agent)
		return m, nil
	case "ctrl+y":
		m.toggleApproval()
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.ta.Value())
		if text == "" {
			return m, nil
		}
		m.ta.Blur()
		if m.scr == scrPlanGoal {
			return m, m.startPlan(text)
		}
		id, err := m.mgr.Spawn(orch.SpawnOpts{
			Task: text, Agent: m.opt.Agent, Model: m.opt.Model,
			Approval: m.opt.Approval, Repo: m.spawnRepo(), Dir: m.opt.Dir, ProjectID: m.projectID(),
		})
		m.scr = scrBoard
		if err != nil {
			m.setFlash("summon failed: "+err.Error(), true)
			return m, nil
		}
		m.lane, m.idx[laneFlight] = laneFlight, 0
		m.setFlash("summoned "+id+" with "+m.opt.Agent, false)
		return m, nil
	}
	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(k)
	return m, cmd
}

func (m *Model) startPlan(goal string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.planCancel, m.planGoal, m.planStart = cancel, goal, time.Now()
	m.scr = scrPlanning
	mgr, ag, model, repo := m.mgr, m.opt.Agent, m.opt.Model, m.spawnRepo()
	return func() tea.Msg {
		tasks, err := mgr.Plan(ctx, goal, ag, model, repo)
		return planDoneMsg{tasks: tasks, err: err}
	}
}

func (m *Model) reviewKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q":
		m.plan = nil
		m.scr = scrBoard
		m.setFlash("plan discarded", false)
	case "up", "k":
		if m.planSel > 0 {
			m.planSel--
		}
	case "down", "j":
		if m.planSel < len(m.plan)-1 {
			m.planSel++
		}
	case " ", "x":
		m.plan[m.planSel].Skip = !m.plan[m.planSel].Skip
	case "tab", "a":
		m.plan[m.planSel].Agent = agent.Next(m.plan[m.planSel].Agent)
	case "ctrl+y", "y":
		m.toggleApproval()
	case "t":
		if m.project == nil {
			m.setFlash("adding to the todo board needs a project: press g on the board", true)
			return m, nil
		}
		n := 0
		for _, tk := range m.plan {
			if tk.Skip {
				continue
			}
			td := store.Todo{ID: store.NewID(), ProjectID: m.project.ID, Title: tk.Title, Body: tk.Task, Agent: tk.Agent, Status: store.TodoOpen, Priority: 1}
			if m.mgr.Store().SaveTodo(td) == nil {
				n++
			}
		}
		m.plan = nil
		m.setFlash(fmt.Sprintf("added %d todo(s) to %s", n, m.project.Name), false)
		return m, m.openTodos()
	case "enter":
		var n int
		var errs []string
		for _, tk := range m.plan {
			if tk.Skip {
				continue
			}
			_, err := m.mgr.Spawn(orch.SpawnOpts{
				Title: tk.Title, Task: tk.Task, Agent: tk.Agent, Model: modelFor(tk.Agent, m.opt),
				Approval: m.opt.Approval, Repo: m.spawnRepo(), Dir: m.opt.Dir, ProjectID: m.projectID(),
			})
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			n++
		}
		m.plan = nil
		m.scr = scrBoard
		m.lane, m.idx[laneFlight] = laneFlight, 0
		if len(errs) > 0 {
			m.setFlash(fmt.Sprintf("summoned %d, %d failed: %s", n, len(errs), errs[0]), true)
		} else {
			m.setFlash(fmt.Sprintf("summoned %d workers: the choir sings", n), false)
		}
	}
	return m, nil
}

func modelFor(agentName string, opt Options) string {
	if agentName == opt.Agent {
		return opt.Model
	}
	return ""
}

func repoLabel(opt Options) string {
	if opt.Repo == "" {
		return "no repo · " + filepath.Base(opt.Dir)
	}
	b := workspace.CurrentBranch(opt.Repo)
	return filepath.Base(opt.Repo) + " ⎇ " + b
}

func (m *Model) repoLabel() string {
	r := m.spawnRepo()
	if r == "" {
		return "no repo · " + filepath.Base(m.opt.Dir)
	}
	return filepath.Base(r) + " ⎇ " + workspace.CurrentBranch(r)
}
