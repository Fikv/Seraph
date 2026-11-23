package orch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"seraph/internal/agent"
	"seraph/internal/store"
	"seraph/internal/workspace"
)

const (
	maxReviewRounds = 2
	maxTestRounds   = 2
	maxDevParallel  = 3
	maxAnalysts     = 4
	diffLimit       = 80_000
	inlineLimit     = 20_000
)

const roleRules = `You are part of a software team run by Seraph. Seraph is the orchestrator: it gives each role a contract, stores every role's output as a markdown artifact the next role reads, and asks the human at the spec, plan and acceptance gates.
Stay inside your role and your contract. Do not do the work of other roles. Your final message is your deliverable: Seraph saves it verbatim as your artifact, so make it complete and self-contained.`

var roleIdentity = map[store.Role]string{
	store.RolePO:         "Your role: Product Owner.",
	store.RoleAnalyst:    "Your role: Analyst. You only read code and answer one question.",
	store.RoleLead:       "Your role: Tech Lead. You plan; you do not write code.",
	store.RoleDev:        "Your role: Developer. You write the code for exactly one task, in your own git worktree.",
	store.RoleReviewer:   "Your role: Code Reviewer with a fresh context. You did not write this code and you do not change it.",
	store.RoleTester:     "Your role: Tester. You write and run tests against the acceptance criteria. You do not fix product code.",
	store.RoleAcceptance: "Your role: Product Owner doing acceptance. You judge the result against the acceptance criteria; you do not change code.",
	store.RoleValidator:  "Your role: Validator, the last gate before this work is merged and pushed. You did not do this work. You run checks; you never change files.",
}

var RoleNames = map[store.Role]string{
	store.RoleOrchestrator: "Orchestrator",
	store.RolePO:           "Product Owner",
	store.RoleAnalyst:      "Analyst",
	store.RoleLead:         "Tech Lead",
	store.RoleDev:          "Developer",
	store.RoleReviewer:     "Reviewer",
	store.RoleTester:       "Tester",
	store.RoleAcceptance:   "Acceptance",
	store.RoleValidator:    "Validator",
}

type RoleConfig struct {
	Agent    string         `json:"agent"`
	Model    string         `json:"model"`
	Approval agent.Approval `json:"approval,omitempty"`
}

func roleKey(r store.Role) string { return "role:" + string(r) }

func readOnlyRole(r store.Role) bool {
	switch r {
	case store.RoleDev, store.RoleTester, store.RoleValidator:
		return false
	}
	return true
}

func (m *Manager) RoleSetting(r store.Role) (RoleConfig, bool) {
	var c RoleConfig
	raw := m.st.Setting(roleKey(r))
	if raw == "" || json.Unmarshal([]byte(raw), &c) != nil || c.Agent == "" {
		return RoleConfig{}, false
	}
	return c, true
}

func (m *Manager) SetRole(r store.Role, c *RoleConfig) error {
	if c == nil {
		return m.st.SetSetting(roleKey(r), "")
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return m.st.SetSetting(roleKey(r), string(b))
}

func (m *Manager) Role(r store.Role) RoleConfig { return m.missionRole("", r) }

func (m *Manager) missionRole(missionID string, r store.Role) RoleConfig {
	d := m.defaults()
	c, ok := m.RoleSetting(r)
	if missionID != "" {
		var mc RoleConfig
		if raw := m.st.Setting(missionRoleKey(missionID, r)); raw != "" && json.Unmarshal([]byte(raw), &mc) == nil && mc.Agent != "" {
			c, ok = mc, true
		}
	}
	if !ok {
		c = RoleConfig{Agent: firstNonEmpty(d.Agent, "claude"), Model: d.Model}
	}
	switch {
	case r == store.RoleValidator:
		c.Approval = agent.ApprovalCheck
	case readOnlyRole(r):
		c.Approval = agent.ApprovalReadOnly
	case c.Approval == "" && d.Approval == agent.ApprovalYolo:
		c.Approval = agent.ApprovalYolo
	case c.Approval == "" && d.Approval == agent.ApprovalConfirm:
		c.Approval = agent.ApprovalConfirm
	case c.Approval == "" || c.Approval == agent.ApprovalSafe:
		c.Approval = agent.ApprovalTest
	}
	return c
}

func (m *Manager) defaults() Defaults {
	m.sched.mu.Lock()
	defer m.sched.mu.Unlock()
	return m.sched.defaults
}

type MissionOpts struct {
	Title     string
	Goal      string
	Repo      string
	ProjectID string
	TodoID    string
	Dev       *RoleConfig
	FromRef   string
}

func missionRoleKey(missionID string, r store.Role) string {
	return "mrole:" + missionID + ":" + string(r)
}

func (m *Manager) StartMission(o MissionOpts) (store.Mission, error) {
	if strings.TrimSpace(o.Goal) == "" {
		return store.Mission{}, errors.New("mission goal is empty")
	}
	repo, err := workspace.RepoRoot(o.Repo)
	if err != nil || o.Repo == "" {
		return store.Mission{}, fmt.Errorf("a mission needs a git repository: %q", o.Repo)
	}
	ms := store.Mission{
		ID:        store.NewID(),
		ProjectID: o.ProjectID,
		TodoID:    o.TodoID,
		Title:     firstNonEmpty(o.Title, titleFrom(o.Goal)),
		Goal:      strings.TrimSpace(o.Goal),
		Repo:      repo,
		Base:      firstNonEmpty(o.FromRef, workspace.CurrentBranch(repo)),
		Phase:     store.PhaseSpec,
		State:     store.MissionRunning,
	}
	dir, branch, err := workspace.CreateFrom(store.Home(), repo, "m-"+ms.ID, firstNonEmpty(o.FromRef, "HEAD"))
	if err != nil {
		return store.Mission{}, err
	}
	ms.Dir, ms.Branch = dir, branch
	ms.Artifacts = m.artifactsDir(ms)
	if err := os.MkdirAll(ms.Artifacts, 0o755); err != nil {
		return store.Mission{}, err
	}
	if err := m.st.SaveMission(ms); err != nil {
		return store.Mission{}, err
	}
	if o.Dev != nil {
		if b, err := json.Marshal(o.Dev); err == nil {
			_ = m.st.SetSetting(missionRoleKey(ms.ID, store.RoleDev), string(b))
		}
	}
	m.writeArtifact(ms, "goal.md", "# "+ms.Title+"\n\n"+ms.Goal+"\n")
	m.Advance(ms.ID)
	got, _, _ := m.st.Mission(ms.ID)
	return got, nil
}

func (m *Manager) artifactsDir(ms store.Mission) string {
	name := time.Now().Format("2006-01-02") + " " + slug(ms.Title) + " " + ms.ID
	if ms.ProjectID != "" {
		if p, ok, _ := m.st.Project(ms.ProjectID); ok && p.NotesDir != "" {
			return filepath.Join(p.NotesDir, "Seraph Missions", name)
		}
	}
	return filepath.Join(store.Home(), "missions", name)
}

var slugRe = regexp.MustCompile(`[^\pL\pN]+`)

func slug(s string) string {
	s = strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if r := []rune(s); len(r) > 40 {
		s = strings.Trim(string(r[:40]), "-")
	}
	if s == "" {
		return "mission"
	}
	return s
}

func (m *Manager) writeArtifact(ms store.Mission, name, content string) string {
	p := filepath.Join(ms.Artifacts, name)
	_ = os.WriteFile(p, []byte(strings.TrimSpace(content)+"\n"), 0o644)
	return p
}

func readFile(p string, limit int) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	s := string(b)
	if limit > 0 && len(s) > limit {
		s = s[:limit] + "\n\n[truncated]"
	}
	return s
}

func (m *Manager) stopping() bool {
	select {
	case <-m.done:
		return true
	default:
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.halting
}

type missionView struct {
	ms    store.Mission
	nodes []store.Node
}

func (v *missionView) byRole(r store.Role, parent string) []*store.Node {
	var out []*store.Node
	for i := range v.nodes {
		n := &v.nodes[i]
		if n.Role == r && (parent == "*" || n.ParentID == parent) {
			out = append(out, n)
		}
	}
	return out
}

func (v *missionView) node(id string) *store.Node {
	for i := range v.nodes {
		if v.nodes[i].ID == id {
			return &v.nodes[i]
		}
	}
	return nil
}

func (v *missionView) last(roles ...store.Role) *store.Node {
	var best *store.Node
	for i := range v.nodes {
		n := &v.nodes[i]
		for _, r := range roles {
			if n.Role == r && (best == nil || n.Position > best.Position) {
				best = n
			}
		}
	}
	return best
}

func (v *missionView) nextPos() int {
	p := 0
	for _, n := range v.nodes {
		p = max(p, n.Position)
	}
	return p + 1
}

func (m *Manager) Mission(id string) (store.Mission, []store.Node, error) {
	ms, ok, err := m.st.Mission(id)
	if err != nil {
		return ms, nil, err
	}
	if !ok {
		return ms, nil, fmt.Errorf("no mission %s", id)
	}
	nodes, err := m.st.Nodes(id)
	return ms, nodes, err
}

func (m *Manager) Advance(id string) {
	if m.stopping() {
		return
	}
	m.mmu.Lock()
	defer m.mmu.Unlock()
	for i := 0; i < 8; i++ {
		ms, nodes, err := m.Mission(id)
		if err != nil || ms.State != store.MissionRunning {
			return
		}
		v := &missionView{ms: ms, nodes: nodes}
		changed, err := m.step(v)
		if err != nil {
			v.ms.State, v.ms.Error = store.MissionBlocked, err.Error()
			changed = true
		}
		if !changed {
			return
		}
		_ = m.st.SaveMission(v.ms)
		m.syncTodo(v.ms)
		m.publishMission()
		if v.ms.State != store.MissionRunning {
			return
		}
	}
}

func (m *Manager) publishMission() {
	m.publish("mission", true)
}

func (m *Manager) step(v *missionView) (bool, error) {
	ms := &v.ms
	switch ms.Phase {
	case store.PhaseSpec:
		po := v.last(store.RolePO)
		if po == nil {
			return true, m.spawnNode(v, store.Node{Role: store.RolePO, Title: "Specification"})
		}
		return m.gateOn(v, po, store.PhaseSpec)

	case store.PhaseAnalysis:
		analysts := v.byRole(store.RoleAnalyst, "*")
		if len(analysts) == 0 {
			qs := analysisQuestions(readFile(v.last(store.RolePO).Artifact, 0))
			if len(qs) == 0 {
				ms.Phase = store.PhasePlan
				return true, nil
			}
			for _, q := range qs {
				if err := m.spawnNode(v, store.Node{Role: store.RoleAnalyst, Title: q, Task: q}); err != nil {
					return true, err
				}
			}
			return true, nil
		}
		for _, n := range analysts {
			switch n.Status {
			case store.NodeFailed:
				return false, nodeErr(n)
			case store.NodeWorking, store.NodePending:
				return false, nil
			}
		}
		ms.Phase = store.PhasePlan
		return true, nil

	case store.PhasePlan:
		lead := v.last(store.RoleLead)
		if lead == nil {
			return true, m.spawnNode(v, store.Node{Role: store.RoleLead, Title: "Plan"})
		}
		if lead.Status == store.NodeDone && len(v.byRole(store.RoleDev, "")) == 0 {
			if err := m.createDevTasks(v, lead); err != nil {
				if lead.Note != formatRetry {
					lead.Note = formatRetry
					return true, m.followNode(lead, fmt.Sprintf(planFormatPrompt, err))
				}
				lead.Status, lead.Note = store.NodeFailed, err.Error()
				_ = m.st.SaveNode(*lead)
				return true, err
			}
		}
		return m.gateOn(v, lead, store.PhasePlan)

	case store.PhaseBuild:
		return m.stepBuild(v)

	case store.PhaseTest:
		return m.stepTest(v)

	case store.PhaseAccept:
		acc := v.last(store.RoleAcceptance)
		lastTest := v.last(store.RoleTester)
		if acc == nil || (lastTest != nil && acc.Position < lastTest.Position) {
			return true, m.spawnNode(v, store.Node{Role: store.RoleAcceptance, Title: "Acceptance"})
		}
		return m.gateOn(v, acc, store.PhaseAccept)
	}
	return false, nil
}

func nodeErr(n *store.Node) error {
	return fmt.Errorf("%s %q failed: %s", RoleNames[n.Role], n.Title, firstNonEmpty(n.Note, "no details"))
}

func (m *Manager) gateOn(v *missionView, n *store.Node, gate store.MissionPhase) (bool, error) {
	switch n.Status {
	case store.NodeDone:
		v.ms.State, v.ms.Gate = store.MissionWaiting, gate
		return true, nil
	case store.NodeFailed:
		return false, nodeErr(n)
	}
	return false, nil
}

func (m *Manager) stepBuild(v *missionView) (bool, error) {
	devs := v.byRole(store.RoleDev, "")
	merged := map[string]bool{}
	running := 0
	for _, d := range devs {
		if d.Status == store.NodeMerged {
			merged[d.ID] = true
		}
		if d.Status == store.NodeWorking {
			running++
		}
	}
	changed := false
	for _, d := range devs {
		switch d.Status {
		case store.NodeFailed:
			return changed, nodeErr(d)
		case store.NodePending:
			ready := running < maxDevParallel
			for _, dep := range d.Deps {
				if !merged[dep] {
					ready = false
				}
			}
			if ready {
				if err := m.runNode(v, d); err != nil {
					return true, err
				}
				running++
				changed = true
			}
		case store.NodeDone:
			c, err := m.reviewOrMerge(v, d)
			if err != nil {
				return true, err
			}
			changed = changed || c
		}
	}
	all := len(devs) > 0
	for _, d := range devs {
		if d.Status != store.NodeMerged {
			all = false
		}
	}
	if all {
		v.ms.Phase = store.PhaseTest
		return true, nil
	}
	return changed, nil
}

func (m *Manager) reviewOrMerge(v *missionView, d *store.Node) (bool, error) {
	var rev *store.Node
	for _, r := range v.byRole(store.RoleReviewer, d.ID) {
		if r.Round == d.Round {
			rev = r
		}
	}
	if rev == nil {
		return true, m.spawnNode(v, store.Node{Role: store.RoleReviewer, ParentID: d.ID, Title: "Review of " + d.Title, Round: d.Round})
	}
	switch rev.Status {
	case store.NodeFailed:
		return false, nodeErr(rev)
	case store.NodeWorking, store.NodePending:
		return false, nil
	}
	if rev.Verdict == "CHANGES" && d.Round < maxReviewRounds {
		d.Round++
		prompt := fmt.Sprintf(devRevisionPrompt, readFile(rev.Artifact, inlineLimit))
		return true, m.followNode(d, prompt)
	}
	sess, ok := m.Get(d.SessionID)
	if !ok {
		return false, fmt.Errorf("developer session for %q is gone", d.Title)
	}
	err := workspace.Merge(v.ms.Dir, sess.Branch, "Merge "+d.Title)
	if err != nil {
		d.Status, d.Note = store.NodeFailed, err.Error()
		_ = m.st.SaveNode(*d)
		return true, err
	}
	d.Status = store.NodeMerged
	if rev.Verdict == "CHANGES" {
		d.Note = fmt.Sprintf("merged after %d review rounds with open findings", d.Round+1)
	}
	return true, m.st.SaveNode(*d)
}

func (m *Manager) stepTest(v *missionView) (bool, error) {
	last := v.last(store.RoleTester, store.RoleDev)
	if last != nil && last.Role == store.RoleDev && last.ParentID == "" {
		last = nil
	}
	if acc := v.last(store.RoleAcceptance); last != nil && acc != nil && acc.Position > last.Position {
		last = nil
	}
	if last == nil {
		return true, m.spawnNode(v, store.Node{Role: store.RoleTester, Title: "Tests"})
	}
	switch last.Status {
	case store.NodeFailed:
		return false, nodeErr(last)
	case store.NodeWorking:
		return false, nil
	case store.NodePending:
		return true, m.runNode(v, last)
	}
	if last.Role == store.RoleDev {
		rounds := len(v.byRole(store.RoleTester, "*"))
		return true, m.spawnNode(v, store.Node{Role: store.RoleTester, Title: fmt.Sprintf("Tests, round %d", rounds+1), Round: rounds})
	}
	if last.Verdict == "FAIL" && last.Round < maxTestRounds {
		return true, m.spawnNode(v, store.Node{Role: store.RoleDev, ParentID: last.ID, Title: "Fix failing tests",
			Task: "The tester reported failures. Fix the product code (or a wrong test) so the acceptance criteria hold.\n\nTester report:\n\n" + readFile(last.Artifact, inlineLimit)})
	}
	v.ms.Phase = store.PhaseAccept
	return true, nil
}

func (m *Manager) createDevTasks(v *missionView, lead *store.Node) error {
	tasks, err := parseLeadTasks(readFile(lead.Artifact, 0))
	if err != nil {
		return err
	}
	ids := map[string]string{}
	for _, t := range tasks {
		ids[t.ID] = store.NewID()
	}
	pos := v.nextPos()
	var created []store.Node
	for i, t := range tasks {
		n := store.Node{ID: ids[t.ID], MissionID: v.ms.ID, Role: store.RoleDev, Title: t.Title, Task: t.Task,
			Files: t.Files, Status: store.NodePending, Position: pos + i}
		for _, d := range t.DependsOn {
			if id, ok := ids[d]; ok {
				n.Deps = append(n.Deps, id)
			}
		}
		if err := m.st.SaveNode(n); err != nil {
			return err
		}
		created = append(created, n)
	}
	v.nodes = append(v.nodes, created...)
	lead.Note = strings.Join(PlanWarnings(created), "\n")
	return m.st.SaveNode(*lead)
}

type leadTask struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Task      string   `json:"task"`
	Files     []string `json:"files"`
	DependsOn []string `json:"depends_on"`
}

var jsonFence = regexp.MustCompile("(?s)```json\\s*(.*?)```")

func parseLeadTasks(s string) ([]leadTask, error) {
	raw := ""
	if all := jsonFence.FindAllStringSubmatch(s, -1); len(all) > 0 {
		raw = all[len(all)-1][1]
	} else if a, b := strings.Index(s, "["), strings.LastIndex(s, "]"); a >= 0 && b > a {
		raw = s[a : b+1]
	}
	var tasks []leadTask
	if raw == "" {
		return nil, errors.New("the tech lead did not end with a JSON task list")
	}
	if err := json.Unmarshal([]byte(raw), &tasks); err != nil {
		if err2 := json.Unmarshal([]byte(repairJSON(raw)), &tasks); err2 != nil {
			return nil, fmt.Errorf("the tech lead's JSON task list is invalid: %v", err)
		}
	}
	var out []leadTask
	for i, t := range tasks {
		if strings.TrimSpace(t.Task) == "" {
			continue
		}
		if t.ID == "" {
			t.ID = fmt.Sprintf("t%d", i+1)
		}
		t.Title = firstNonEmpty(t.Title, titleFrom(t.Task))
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil, errors.New("the tech lead's task list is empty")
	}
	return out, nil
}

func repairJSON(s string) string {
	var b strings.Builder
	inString := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inString = !inString
			b.WriteByte(c)
		case c == '\\' && inString && i+1 < len(s):
			next := s[i+1]
			switch {
			case strings.IndexByte(`"\/bfnrt`, next) >= 0:
				b.WriteByte(c)
				b.WriteByte(next)
				i++
			case next == 'u' && i+5 < len(s) && isHex(s[i+2:i+6]):
				b.WriteString(s[i : i+6])
				i += 5
			default:
				b.WriteString(`\\`)
			}
		case inString && c == '\n':
			b.WriteString(`\n`)
		case inString && c == '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

func globPrefix(p string) string {
	p = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(p)), "./")
	if i := strings.IndexAny(p, "*?[{"); i >= 0 {
		p = p[:i]
	}
	return p
}

func pathsOverlap(a, b string) bool {
	pa, pb := globPrefix(a), globPrefix(b)
	if pa == "" || pb == "" {
		return true
	}
	return strings.HasPrefix(pa, pb) || strings.HasPrefix(pb, pa)
}

func dependsOn(nodes []store.Node, a, b string) bool {
	seen := map[string]bool{}
	var walk func(id string) bool
	walk = func(id string) bool {
		if id == b {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		for _, n := range nodes {
			if n.ID == id {
				for _, d := range n.Deps {
					if walk(d) {
						return true
					}
				}
			}
		}
		return false
	}
	return walk(a)
}

func PlanWarnings(devs []store.Node) []string {
	var out []string
	for i, a := range devs {
		if len(a.Files) == 0 {
			out = append(out, fmt.Sprintf("%q owns no paths: it may collide with other tasks", a.Title))
		}
		for _, b := range devs[i+1:] {
			if dependsOn(devs, a.ID, b.ID) || dependsOn(devs, b.ID, a.ID) {
				continue
			}
			for _, fa := range a.Files {
				for _, fb := range b.Files {
					if pathsOverlap(fa, fb) {
						out = append(out, fmt.Sprintf("%q and %q both own %s / %s and run in parallel", a.Title, b.Title, fa, fb))
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

var questionLine = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+(.+)$`)

func analysisQuestions(spec string) []string {
	var out []string
	in := false
	for _, l := range strings.Split(spec, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			in = strings.Contains(strings.ToLower(l), "analysis question")
			continue
		}
		if !in {
			continue
		}
		mt := questionLine.FindStringSubmatch(l)
		if mt == nil {
			continue
		}
		q := strings.TrimSpace(mt[1])
		if q == "" || strings.EqualFold(strings.Trim(q, ".*_ "), "none") {
			continue
		}
		out = append(out, q)
		if len(out) == maxAnalysts {
			break
		}
	}
	return out
}

var verdictRe = regexp.MustCompile(`(?i)VERDICT:\s*\**\s*([A-Z_]+)`)

func parseVerdict(s string) string {
	all := verdictRe.FindAllStringSubmatch(s, -1)
	if len(all) == 0 {
		return ""
	}
	return strings.ToUpper(all[len(all)-1][1])
}

func (m *Manager) spawnNode(v *missionView, n store.Node) error {
	n.ID = firstNonEmpty(n.ID, store.NewID())
	n.MissionID = v.ms.ID
	n.Status = store.NodePending
	n.Position = v.nextPos()
	if err := m.st.SaveNode(n); err != nil {
		return err
	}
	v.nodes = append(v.nodes, n)
	return m.runNode(v, v.node(n.ID))
}

func (m *Manager) runNode(v *missionView, n *store.Node) error {
	cfg := m.missionRole(v.ms.ID, n.Role)
	prompt, err := m.nodePrompt(v, n)
	if err != nil {
		return err
	}
	o := SpawnOpts{
		Title:     RoleNames[n.Role] + ": " + n.Title,
		Task:      prompt,
		Agent:     cfg.Agent,
		Model:     cfg.Model,
		Approval:  cfg.Approval,
		ProjectID: v.ms.ProjectID,
		MissionID: v.ms.ID,
		NodeID:    n.ID,
	}
	switch {
	case n.Role == store.RoleDev && n.ParentID == "":
		o.Repo, o.FromRef = v.ms.Repo, v.ms.Branch
	case n.Role == store.RoleReviewer:
		dev := v.node(n.ParentID)
		sess, ok := m.Get(dev.SessionID)
		if !ok {
			return fmt.Errorf("no developer session to review for %q", dev.Title)
		}
		o.Shared, o.Repo, o.Dir, o.Branch, o.Base = true, v.ms.Repo, sess.Dir, sess.Branch, sess.Base
	default:
		o.Shared, o.Repo, o.Dir, o.Branch, o.Base = true, v.ms.Repo, v.ms.Dir, v.ms.Branch, v.ms.Base
	}
	n.Status, n.Note, n.Verdict = store.NodeWorking, "", ""
	if err := m.st.SaveNode(*n); err != nil {
		return err
	}
	id, err := m.Spawn(o)
	if err != nil {
		n.Status, n.Note = store.NodeFailed, err.Error()
		_ = m.st.SaveNode(*n)
		return err
	}
	n.SessionID = id
	return m.st.SaveNode(*n)
}

func (m *Manager) followNode(n *store.Node, prompt string) error {
	n.Status, n.Verdict = store.NodeWorking, ""
	if err := m.st.SaveNode(*n); err != nil {
		return err
	}
	if err := m.Followup(n.SessionID, prompt); err != nil {
		n.Status, n.Note = store.NodeFailed, err.Error()
		_ = m.st.SaveNode(*n)
		return err
	}
	return nil
}

func (m *Manager) nodeSystem(s store.Session) (string, []string) {
	ms, ok, _ := m.st.Mission(s.MissionID)
	if !ok {
		return fmt.Sprintf(workerPrompt, branchOr(s.Branch)), nil
	}
	var role store.Role
	for _, n := range must(m.st.Nodes(ms.ID)) {
		if n.ID == s.NodeID {
			role = n.Role
		}
	}
	sys := roleRules + "\n" + roleIdentity[role]
	if role == store.RoleDev {
		sys += "\n" + fmt.Sprintf(workerPrompt, branchOr(s.Branch))
	}
	return sys, []string{ms.Artifacts}
}

func must[T any](v T, _ error) T { return v }

func (m *Manager) nodeSessionEnded(s store.Session) {
	ms, ok, _ := m.st.Mission(s.MissionID)
	if !ok {
		return
	}
	var node *store.Node
	nodes, _ := m.st.Nodes(ms.ID)
	for i := range nodes {
		if nodes[i].ID == s.NodeID {
			node = &nodes[i]
		}
	}
	if node == nil || node.Status != store.NodeWorking || node.SessionID != s.ID {
		return
	}
	text := ""
	if s.Status == store.StatusDone {
		if text = m.resultText(s.ID); strings.TrimSpace(text) == "" {
			s.Status, s.Error = store.StatusFailed, "the agent finished without a deliverable"
		}
	}
	if s.Status != store.StatusDone {
		node.Status, node.Note = store.NodeFailed, firstNonEmpty(s.Error, string(s.Status))
		_ = m.st.SaveNode(*node)
		if ms.State == store.MissionRunning {
			ms.State, ms.Error = store.MissionBlocked, nodeErr(node).Error()
			_ = m.st.SaveMission(ms)
			m.syncTodo(ms)
		}
		m.publishMission()
		return
	}
	node.Verdict = parseVerdict(text)
	node.Artifact = m.writeArtifact(ms, artifactName(*node, nodes), text)
	node.Status = store.NodeDone
	_ = m.st.SaveNode(*node)
	m.publishMission()
	m.Advance(ms.ID)
}

func artifactName(n store.Node, all []store.Node) string {
	idx := 0
	for _, o := range all {
		if o.Role == n.Role && o.Position <= n.Position {
			idx++
		}
	}
	switch n.Role {
	case store.RolePO:
		return "spec.md"
	case store.RoleLead:
		return "plan.md"
	case store.RoleAcceptance:
		return fmt.Sprintf("acceptance-%d.md", idx)
	case store.RoleTester:
		return fmt.Sprintf("tests-%d.md", idx)
	case store.RoleAnalyst:
		return fmt.Sprintf("analysis-%d-%s.md", idx, slug(n.Title))
	case store.RoleReviewer:
		return fmt.Sprintf("review-%s-r%d.md", slug(strings.TrimPrefix(n.Title, "Review of ")), n.Round+1)
	}
	return fmt.Sprintf("dev-%d-%s.md", idx, slug(n.Title))
}

func (m *Manager) ResultText(id string) string { return m.resultText(id) }

func (m *Manager) resultText(id string) string {
	evs, _ := m.Log(id)
	lastUser := 0
	for i, ev := range evs {
		if ev.Kind == agent.KindUser {
			lastUser = i
		}
	}
	var text strings.Builder
	result := ""
	for _, ev := range evs[lastUser:] {
		switch ev.Kind {
		case agent.KindText:
			text.WriteString(ev.Text + "\n")
		case agent.KindResult:
			if strings.TrimSpace(ev.Text) != "" {
				result = ev.Text
			}
		}
	}
	if result != "" {
		return result
	}
	return text.String()
}

func (m *Manager) Approve(id string) error {
	m.mmu.Lock()
	ms, ok, err := m.st.Mission(id)
	if err != nil || !ok {
		m.mmu.Unlock()
		return fmt.Errorf("no mission %s", id)
	}
	if ms.State != store.MissionWaiting {
		m.mmu.Unlock()
		return errors.New("the mission is not waiting for a decision")
	}
	switch ms.Gate {
	case store.PhaseSpec:
		ms.Phase = store.PhaseAnalysis
	case store.PhasePlan:
		ms.Phase = store.PhaseBuild
	case store.PhaseAccept:
		ms.Phase, ms.State = store.PhaseDone, store.MissionDone
	}
	if ms.State == store.MissionWaiting {
		ms.State = store.MissionRunning
	}
	ms.Gate, ms.Error = "", ""
	err = m.st.SaveMission(ms)
	m.syncTodo(ms)
	m.mmu.Unlock()
	m.publishMission()
	m.Advance(id)
	return err
}

const formatRetry = "asked to fix the task list format"
const planFormatPrompt = "Seraph could not read your task list: %v\n\nReply again with the full plan, ending with the fenced json block. It must be strictly valid JSON: escape only with \\\" \\\\ \\n \\t or \\uXXXX (no \\U or \\x escapes); write non-ASCII characters directly."
const specRevisionPrompt = "The human asked for changes to the specification:\n\n%s\n\nReply with the full revised specification in the same format as before."
const planRevisionPrompt = "The human asked for changes to the plan:\n\n%s\n\nReply with the full revised plan in the same format, ending with the complete JSON task list."
const devRevisionPrompt = `A reviewer with fresh eyes reviewed your change. Fix the [blocking] findings below; take [minor] ones when cheap. If a finding contradicts your task or would make you edit outside your owned paths, do not apply it and say why in your report.

Review:

%s

End with a short report of what you changed in response.`

func (m *Manager) Revise(id, feedback string) error {
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return errors.New("say what should change")
	}
	m.mmu.Lock()
	ms, nodes, err := m.Mission(id)
	if err != nil {
		m.mmu.Unlock()
		return err
	}
	if ms.State != store.MissionWaiting {
		m.mmu.Unlock()
		return errors.New("the mission is not waiting for a decision")
	}
	v := &missionView{ms: ms, nodes: nodes}
	gate := ms.Gate
	ms.State, ms.Gate, ms.Error = store.MissionRunning, "", ""
	if err := m.st.SaveMission(ms); err != nil {
		m.mmu.Unlock()
		return err
	}
	switch gate {
	case store.PhaseSpec:
		po := v.last(store.RolePO)
		po.Round++
		err = m.followNode(po, fmt.Sprintf(specRevisionPrompt, feedback))
	case store.PhasePlan:
		lead := v.last(store.RoleLead)
		for _, d := range v.byRole(store.RoleDev, "") {
			_ = m.st.DeleteNode(d.ID)
		}
		lead.Round++
		err = m.followNode(lead, fmt.Sprintf(planRevisionPrompt, feedback))
	case store.PhaseAccept:
		acc := v.last(store.RoleAcceptance)
		ms.Phase = store.PhaseTest
		_ = m.st.SaveMission(ms)
		err = m.spawnNode(v, store.Node{Role: store.RoleDev, ParentID: acc.ID, Title: "Acceptance changes",
			Task: "The human reviewed the result and asked for these changes:\n\n" + feedback})
	}
	m.syncTodo(ms)
	m.mmu.Unlock()
	m.publishMission()
	if err != nil {
		m.block(id, err)
	}
	return err
}

func (m *Manager) block(id string, err error) {
	ms, ok, _ := m.st.Mission(id)
	if !ok {
		return
	}
	ms.State, ms.Error = store.MissionBlocked, err.Error()
	_ = m.st.SaveMission(ms)
	m.syncTodo(ms)
	m.publishMission()
}

func (m *Manager) RetryNode(missionID, nodeID string) error {
	m.mmu.Lock()
	ms, nodes, err := m.Mission(missionID)
	if err != nil {
		m.mmu.Unlock()
		return err
	}
	v := &missionView{ms: ms, nodes: nodes}
	n := v.node(nodeID)
	if n == nil {
		for i := range v.nodes {
			if v.nodes[i].Status == store.NodeFailed {
				n = &v.nodes[i]
			}
		}
	}
	if n == nil {
		m.mmu.Unlock()
		return errors.New("nothing failed in this mission")
	}
	for _, c := range v.nodes {
		if c.ParentID == n.ID && c.Role == store.RoleReviewer {
			_ = m.st.DeleteNode(c.ID)
		}
	}
	if n.Role == store.RoleDev && n.SessionID != "" {
		_ = m.Remove(n.SessionID)
	}
	n.Round, n.SessionID = 0, ""
	ms.State, ms.Error = store.MissionRunning, ""
	_ = m.st.SaveMission(ms)
	v.ms = ms
	err = m.runNode(v, n)
	m.syncTodo(ms)
	m.mmu.Unlock()
	m.publishMission()
	if err != nil {
		m.block(missionID, err)
		return err
	}
	m.Advance(missionID)
	return nil
}

func (m *Manager) CancelMission(id string) error {
	m.mmu.Lock()
	ms, nodes, err := m.Mission(id)
	if err != nil {
		m.mmu.Unlock()
		return err
	}
	ms.State, ms.Gate = store.MissionCancelled, ""
	_ = m.st.SaveMission(ms)
	m.syncTodo(ms)
	m.mmu.Unlock()
	for _, n := range nodes {
		if n.Status == store.NodeWorking && n.SessionID != "" {
			m.Stop(n.SessionID)
		}
	}
	m.publishMission()
	return nil
}

func (m *Manager) RemoveMission(id string) error {
	ms, nodes, err := m.Mission(id)
	if err != nil {
		return err
	}
	if ms.Active() {
		_ = m.CancelMission(id)
	}
	for _, n := range nodes {
		if n.SessionID != "" {
			m.Stop(n.SessionID)
			_ = m.Remove(n.SessionID)
		}
	}
	if ms.Dir != "" {
		_ = workspace.Remove(ms.Repo, ms.Dir)
	}
	err = m.st.DeleteMission(id)
	m.publishMission()
	return err
}

func (m *Manager) Missions() ([]store.Mission, error) { return m.st.Missions() }

func (m *Manager) syncTodo(ms store.Mission) {
	if ms.TodoID == "" {
		return
	}
	t, ok, _ := m.st.Todo(ms.TodoID)
	if !ok {
		return
	}
	note := "mission " + ms.ID + ": " + string(ms.Phase)
	switch {
	case ms.State == store.MissionDone:
		t.Status, note = store.TodoDone, "mission "+ms.ID+" accepted, branch "+ms.Branch
	case ms.State == store.MissionCancelled:
		t.Status, note = store.TodoOpen, "mission "+ms.ID+" cancelled"
	case ms.State == store.MissionWaiting && ms.Gate == store.PhaseAccept:
		t.Status, note = store.TodoReview, "mission "+ms.ID+" waits for acceptance"
	case ms.State == store.MissionWaiting:
		t.Status, note = store.TodoDoing, "mission "+ms.ID+" waits for you: "+string(ms.Gate)
	case ms.State == store.MissionBlocked:
		t.Status, note = store.TodoDoing, "mission "+ms.ID+" needs you: "+ms.Error
	default:
		t.Status = store.TodoDoing
	}
	if t.Status == store.TodoDoing || t.Status == store.TodoReview {
		t.SessionID = ""
	}
	t.Note = note
	_ = m.st.SaveTodo(t)
}

func (m *Manager) tickMissions() {
	missions, err := m.st.Missions()
	if err != nil {
		return
	}
	for _, ms := range missions {
		if !ms.Active() {
			continue
		}
		nodes, _ := m.st.Nodes(ms.ID)
		for _, n := range nodes {
			if n.Status != store.NodeWorking || n.SessionID == "" {
				continue
			}
			s, ok := m.Get(n.SessionID)
			switch {
			case !ok && time.Since(n.UpdatedAt) > staleAfter:
				m.nodeSessionEnded(store.Session{ID: n.SessionID, MissionID: ms.ID, NodeID: n.ID, Status: store.StatusFailed, Error: "worker session disappeared"})
			case ok && !m.Active(s.ID) && s.Status != store.StatusWorking && s.Status != store.StatusQueued:
				m.nodeSessionEnded(s)
			}
		}
		if ms.State == store.MissionRunning {
			m.Advance(ms.ID)
		}
	}
}
