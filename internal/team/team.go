package team

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"seraph/internal/agent"
	"seraph/internal/store"
	"seraph/internal/workspace"
)

const (
	ServerName = "seraph_team"
	ToolPrefix = "mcp__" + ServerName
	MaxDepth   = 2
	DirSetting = "team_dir:"
	NotesFile  = "TEAM.md"
)

type Server struct {
	Store     *store.Store
	SessionID string
	Poll      time.Duration
	MaxWait   time.Duration
}

type tool struct {
	name, desc string
	props      map[string]any
	required   []string
	minDepth   int
	maxDepth   int
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

var tools = []tool{
	{name: "list_agents", desc: "Show the whole team as a tree: every agent's id, name, role, status, model and last activity.", maxDepth: 9},
	{name: "read_notes", desc: "Read TEAM.md, the shared team notebook: goal, decisions, findings and results. Read it before you start.", maxDepth: 9},
	{name: "write_note", desc: "Append a finding, decision or warning to TEAM.md so every agent knows it. Keep it short and factual.", maxDepth: 9,
		props: map[string]any{"text": str("markdown to append")}, required: []string{"text"}},
	{name: "message", desc: "Send a message to another agent by name or id, or to \"parent\". Delivered when they finish their current step.", maxDepth: 9,
		props: map[string]any{"to": str("agent name, id or parent"), "text": str("the message")}, required: []string{"to", "text"}},
	{name: "spawn_agent", desc: "Create a subagent with its own git worktree branched from your branch, and give it a task. Returns at once; use wait_agents to collect the result.", maxDepth: MaxDepth - 1,
		props: map[string]any{
			"name":     str("short unique name, e.g. api-dev"),
			"role":     str("role, e.g. analyst, developer, tester, reviewer"),
			"task":     str("the complete brief: objective, context, output format, what not to do"),
			"paths":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "paths or globs this agent owns; parallel agents must not overlap"},
			"readonly": map[string]any{"type": "boolean", "description": "true for research or review agents that must not change files"},
			"model":    str("optional model id or alias (haiku, sonnet, opus); default: yours"),
		}, required: []string{"name", "task"}},
	{name: "wait_agents", desc: "Wait until the given agents (default: all your running children) finish, up to timeout_s seconds (max 300). Returns each one's status and report.", maxDepth: MaxDepth - 1,
		props: map[string]any{"agents": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "timeout_s": map[string]any{"type": "number"}}},
	{name: "read_result", desc: "Read an agent's full final report and the files it changed.", maxDepth: 9,
		props: map[string]any{"agent": str("name or id")}, required: []string{"agent"}},
	{name: "merge_agent", desc: "Merge a finished child's branch (or a finished mission: mission:<id>) into your branch. On conflict you get the files; delegate a fix.", maxDepth: MaxDepth - 1,
		props: map[string]any{"agent": str("name, id or mission:<id>")}, required: []string{"agent"}},
	{name: "stop_agent", desc: "Stop one of your children.", maxDepth: MaxDepth - 1,
		props: map[string]any{"agent": str("name or id")}, required: []string{"agent"}},
	{name: "start_mission", desc: "Start a full mission (PO spec → analysts → plan → developers with review → tests → acceptance) branched from your branch. The human approves its gates in Seraph. Use for big, well-defined features.", maxDepth: 0,
		props: map[string]any{"goal": str("what the user should get"), "title": str("short title")}, required: []string{"goal"}},
	{name: "mission_status", desc: "Phase, state and artifacts of a mission you started.", maxDepth: 0,
		props: map[string]any{"id": str("mission id")}, required: []string{"id"}},
}

type rpc struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

func (s Server) self() (store.Session, error) {
	x, ok, err := s.Store.Get(s.SessionID)
	if err != nil || !ok {
		return x, fmt.Errorf("unknown session %s", s.SessionID)
	}
	return x, nil
}

func Depth(st *store.Store, x store.Session) int {
	d := 0
	for x.ParentID != "" && d < 10 {
		p, ok, _ := st.Get(x.ParentID)
		if !ok {
			break
		}
		x = p
		d++
	}
	return d
}

func (s Server) Serve(r io.Reader, w io.Writer) error {
	if s.Poll == 0 {
		s.Poll = 300 * time.Millisecond
	}
	if s.MaxWait == 0 {
		s.MaxWait = 300 * time.Second
	}
	enc := json.NewEncoder(w)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var req rpc
		if json.Unmarshal(sc.Bytes(), &req) != nil || len(req.ID) == 0 {
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			result = map[string]any{"protocolVersion": firstNonEmpty(p.ProtocolVersion, "2025-06-18"),
				"capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": ServerName, "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": s.toolList()}
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			text, err := s.Call(p.Name, p.Arguments)
			res := map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}
			if err != nil {
				res = map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": err.Error()}}}
			}
			result = res
		case "ping":
			result = map[string]any{}
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
			continue
		}
		if err := enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); err != nil {
			return err
		}
	}
	return sc.Err()
}

func (s Server) toolList() []any {
	me, err := s.self()
	depth := 0
	if err == nil {
		depth = Depth(s.Store, me)
	}
	var out []any
	for _, t := range tools {
		if depth > t.maxDepth || depth < t.minDepth {
			continue
		}
		schema := map[string]any{"type": "object", "properties": map[string]any{}}
		if t.props != nil {
			schema["properties"] = t.props
		}
		if len(t.required) > 0 {
			schema["required"] = t.required
		}
		out = append(out, map[string]any{"name": t.name, "description": t.desc, "inputSchema": schema})
	}
	return out
}

func (s Server) Call(name string, args map[string]any) (string, error) {
	me, err := s.self()
	if err != nil {
		return "", err
	}
	depth := Depth(s.Store, me)
	for _, t := range tools {
		if t.name == name && depth > t.maxDepth {
			return "", fmt.Errorf("%s is not available at your depth in the team (max depth %d)", name, MaxDepth)
		}
	}
	switch name {
	case "list_agents":
		return Tree(s.Store, me.TeamID, me.ID), nil
	case "read_notes":
		return ReadNotes(s.Store, me.TeamID), nil
	case "write_note":
		text := strArg(args, "text")
		if text == "" {
			return "", errors.New("text is empty")
		}
		return "noted in " + NotesFile, AppendNote(s.Store, me.TeamID, displayName(me), text)
	case "message":
		return s.message(me, strArg(args, "to"), strArg(args, "text"))
	case "spawn_agent":
		return s.request(me, "spawn", args, 90*time.Second)
	case "wait_agents":
		return s.wait(me, listArg(args, "agents"), numArg(args, "timeout_s"))
	case "read_result":
		x, err := s.resolve(me, strArg(args, "agent"))
		if err != nil {
			return "", err
		}
		return Report(s.Store, x), nil
	case "merge_agent":
		return s.merge(me, strArg(args, "agent"))
	case "stop_agent":
		x, err := s.child(me, strArg(args, "agent"))
		if err != nil {
			return "", err
		}
		return "stop requested for " + displayName(x), s.Store.RequestStop(x.ID, true)
	case "start_mission":
		return s.request(me, "mission", args, 60*time.Second)
	case "mission_status":
		return MissionStatus(s.Store, strArg(args, "id"))
	}
	return "", fmt.Errorf("unknown tool %s", name)
}

func (s Server) request(me store.Session, op string, args map[string]any, wait time.Duration) (string, error) {
	id := store.NewID() + store.NewID()
	if err := s.Store.AddTeamRequest(store.TeamRequest{ID: id, TeamID: me.TeamID, FromSession: me.ID, Op: op, Args: args}); err != nil {
		return "", err
	}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		r, ok, _ := s.Store.TeamRequest(id)
		if ok && r.Status == store.ReqDone {
			return r.Result, nil
		}
		if ok && r.Status == store.ReqError {
			return "", errors.New(r.Result)
		}
		time.Sleep(s.Poll)
	}
	return "", errors.New("Seraph did not handle the request in time: is the Seraph board (TUI) open?")
}

func (s Server) message(me store.Session, to, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", errors.New("text is empty")
	}
	var target store.Session
	if to == "parent" || to == "orchestrator" {
		id := me.TeamID
		if to == "parent" {
			id = me.ParentID
		}
		p, ok, _ := s.Store.Get(id)
		if !ok || id == "" {
			return "", errors.New("you have no " + to)
		}
		target = p
	} else {
		x, err := s.resolve(me, to)
		if err != nil {
			return "", err
		}
		target = x
	}
	if target.ID == me.ID {
		return "", errors.New("that is you")
	}
	if err := s.Store.AddTeamMessage(store.TeamMessage{ID: store.NewID() + store.NewID(), TeamID: me.TeamID, FromSession: me.ID, ToSession: target.ID, Text: text}); err != nil {
		return "", err
	}
	_ = s.Store.AddTeamRequest(store.TeamRequest{ID: store.NewID() + store.NewID(), TeamID: me.TeamID, FromSession: me.ID, Op: "deliver", Args: map[string]any{"to": target.ID}})
	return "queued for " + displayName(target) + "; delivered when they finish their current step", nil
}

func active(x store.Session) bool {
	return x.Status == store.StatusWorking || x.Status == store.StatusQueued
}

func (s Server) wait(me store.Session, names []string, timeout float64) (string, error) {
	var ids []string
	if len(names) == 0 {
		all, _ := s.Store.TeamSessions(me.TeamID)
		for _, x := range all {
			if x.ParentID == me.ID && (active(x) || !s.Store.Reported(x.ID)) {
				ids = append(ids, x.ID)
			}
		}
		if len(ids) == 0 {
			return "you have no running or unreported children", nil
		}
	}
	for _, n := range names {
		x, err := s.resolve(me, n)
		if err != nil {
			return "", err
		}
		ids = append(ids, x.ID)
	}
	limit := s.MaxWait
	if timeout > 0 && time.Duration(timeout*float64(time.Second)) < limit {
		limit = time.Duration(timeout * float64(time.Second))
	}
	deadline := time.Now().Add(limit)
	for {
		running := 0
		for _, id := range ids {
			if x, ok, _ := s.Store.Get(id); ok && active(x) {
				running++
			}
		}
		if running == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(s.Poll)
	}
	var b strings.Builder
	for _, id := range ids {
		x, ok, _ := s.Store.Get(id)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "## %s (%s) · %s\n", displayName(x), x.ID, x.Status)
		if active(x) {
			fmt.Fprintf(&b, "still working: %s\n\n", oneLine(x.LastLine, 200))
			continue
		}
		b.WriteString(clip(ResultText(s.Store, x.ID), 3000) + "\n\n")
		_ = s.Store.MarkReported(x.ID, true)
	}
	if b.Len() == 0 {
		return "nothing to report", nil
	}
	return b.String(), nil
}

func (s Server) merge(me store.Session, ref string) (string, error) {
	branch, label := "", ""
	if strings.HasPrefix(ref, "mission:") {
		ms, ok, _ := s.Store.Mission(strings.TrimPrefix(ref, "mission:"))
		if !ok {
			return "", fmt.Errorf("no mission %s", ref)
		}
		if ms.State != store.MissionDone {
			return "", fmt.Errorf("mission %s is %s (%s); merge it after the human accepts it", ms.ID, ms.State, ms.Phase)
		}
		branch, label = ms.Branch, "mission "+ms.Title
	} else {
		x, err := s.child(me, ref)
		if err != nil {
			return "", err
		}
		if active(x) {
			return "", fmt.Errorf("%s is still working", displayName(x))
		}
		if x.Branch == "" {
			return "", fmt.Errorf("%s has no branch", displayName(x))
		}
		branch, label = x.Branch, displayName(x)
	}
	if dirty, _ := workspace.DirtyTracked(me.Dir); dirty != "" {
		if _, err := workspace.CommitAll(me.Dir, "wip before merging "+label); err != nil {
			return "", err
		}
	}
	if err := workspace.Merge(me.Dir, branch, "Merge "+label); err != nil {
		var c *workspace.ConflictError
		if errors.As(err, &c) {
			return "", fmt.Errorf("merge conflict in %s; the merge was aborted. Ask the owner of those files to rebuild on your branch, or resolve it yourself", strings.Join(c.Files, ", "))
		}
		return "", err
	}
	stat := workspace.DiffText(me.Dir, "HEAD~1..HEAD", 0)
	if i := strings.Index(stat, "\ndiff --git"); i > 0 {
		stat = stat[:i]
	}
	_ = AppendNote(s.Store, me.TeamID, "Seraph", "Merged "+label+" into "+me.Branch+".")
	return "merged " + label + " into " + me.Branch + "\n" + strings.TrimSpace(stat), nil
}

func (s Server) resolve(me store.Session, ref string) (store.Session, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return store.Session{}, errors.New("say which agent: pass its name or id in \"agent\" (see list_agents)")
	}
	all, err := s.Store.TeamSessions(me.TeamID)
	if err != nil {
		return store.Session{}, err
	}
	for _, x := range all {
		if x.ID == ref || strings.EqualFold(x.AgentName, ref) {
			return x, nil
		}
	}
	return store.Session{}, fmt.Errorf("no agent %q in this team (see list_agents)", ref)
}

func (s Server) child(me store.Session, ref string) (store.Session, error) {
	x, err := s.resolve(me, ref)
	if err != nil {
		return x, err
	}
	if x.ParentID != me.ID {
		return x, fmt.Errorf("%s is not your child", displayName(x))
	}
	return x, nil
}

func displayName(x store.Session) string {
	if x.AgentName != "" {
		return x.AgentName
	}
	if x.Role == "orchestrator" {
		return "orchestrator"
	}
	return x.ID
}

func DisplayName(x store.Session) string { return displayName(x) }

func Tree(st *store.Store, teamID, me string) string {
	all, _ := st.TeamSessions(teamID)
	kids := map[string][]store.Session{}
	for _, x := range all {
		kids[x.ParentID] = append(kids[x.ParentID], x)
	}
	var b strings.Builder
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		for _, x := range kids[parent] {
			if x.ID != teamID && parent == "" {
				continue
			}
			you := ""
			if x.ID == me {
				you = " ← you"
			}
			model := x.Model
			if model == "" {
				model = "default"
			}
			fmt.Fprintf(&b, "%s- %s (%s) · %s · %s · %s/%s%s\n", strings.Repeat("  ", depth), displayName(x), x.ID, roleOf(x), x.Status, x.Agent, model, you)
			if line := oneLine(firstNonEmpty(x.LastLine, x.Title), 160); line != "" {
				fmt.Fprintf(&b, "%s  last: %s\n", strings.Repeat("  ", depth), line)
			}
			walk(x.ID, depth+1)
		}
	}
	walk("", 0)
	return strings.TrimSpace(b.String())
}

func roleOf(x store.Session) string {
	if x.Role == "orchestrator" {
		return "orchestrator"
	}
	t := x.Title
	if i := strings.Index(t, ":"); i > 0 {
		return strings.TrimSpace(t[:i])
	}
	return "agent"
}

func Dir(st *store.Store, teamID string) string { return st.Setting(DirSetting + teamID) }

func ReadNotes(st *store.Store, teamID string) string {
	b, err := os.ReadFile(filepath.Join(Dir(st, teamID), NotesFile))
	if err != nil {
		return "(no notes yet)"
	}
	return clip(string(b), 30000)
}

func AppendNote(st *store.Store, teamID, who, text string) error {
	dir := Dir(st, teamID)
	if dir == "" {
		return errors.New("this team has no notes folder")
	}
	f, err := os.OpenFile(filepath.Join(dir, NotesFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "\n### %s · %s\n\n%s\n", who, time.Now().Format("2006-01-02 15:04"), strings.TrimSpace(text))
	return err
}

func ResultText(st *store.Store, id string) string {
	evs, _ := st.ReadLog(id)
	last := 0
	for i, ev := range evs {
		if ev.Kind == agent.KindUser {
			last = i
		}
	}
	var text strings.Builder
	result := ""
	for _, ev := range evs[last:] {
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
	if t := strings.TrimSpace(text.String()); t != "" {
		return t
	}
	return "(no report)"
}

func Report(st *store.Store, x store.Session) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (%s) · %s\n\n", displayName(x), x.ID, x.Status)
	if x.Error != "" {
		fmt.Fprintf(&b, "error: %s\n\n", x.Error)
	}
	b.WriteString(clip(ResultText(st, x.ID), 12000))
	if x.Branch != "" && x.Base != "" && x.Repo != "" {
		stat := workspace.DiffText(x.Repo, x.Base+"..."+x.Branch, 0)
		if i := strings.Index(stat, "\ndiff --git"); i > 0 {
			stat = stat[:i]
		}
		fmt.Fprintf(&b, "\n\n## Changes on %s (vs %s)\n%s", x.Branch, x.Base, clip(strings.TrimSpace(stat), 4000))
	}
	return b.String()
}

func MissionStatus(st *store.Store, id string) (string, error) {
	ms, ok, _ := st.Mission(id)
	if !ok {
		return "", fmt.Errorf("no mission %s", id)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "mission %s · %s\nphase %s · state %s", ms.ID, ms.Title, ms.Phase, ms.State)
	if ms.Gate != "" {
		fmt.Fprintf(&b, " · waiting for the human at the %s gate", ms.Gate)
	}
	if ms.Error != "" {
		fmt.Fprintf(&b, "\nerror: %s", ms.Error)
	}
	fmt.Fprintf(&b, "\nbranch %s · artifacts %s", ms.Branch, ms.Artifacts)
	if ents, err := os.ReadDir(ms.Artifacts); err == nil {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		fmt.Fprintf(&b, "\nfiles: %s", strings.Join(names, ", "))
	}
	return b.String(), nil
}

var argAliases = map[string][]string{
	"agent": {"agent", "name", "id", "agent_name", "agent_id", "agentName", "agentId", "target"},
	"text":  {"text", "note", "content", "message", "body", "markdown"},
	"task":  {"task", "brief", "prompt", "instructions", "description", "objective"},
	"name":  {"name", "agent_name", "agentName", "id"},
	"to":    {"to", "agent", "recipient", "target", "name"},
	"goal":  {"goal", "task", "description", "objective"},
	"id":    {"id", "mission_id", "missionId", "mission"},
}

func strArg(a map[string]any, k string) string {
	keys := argAliases[k]
	if len(keys) == 0 {
		keys = []string{k}
	}
	for _, key := range keys {
		if v, ok := a[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func numArg(a map[string]any, k string) float64 {
	v, _ := a[k].(float64)
	return v
}

func listArg(a map[string]any, k string) []string {
	var out []string
	if xs, ok := a[k].([]any); ok {
		for _, x := range xs {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	}
	return out
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "\n[truncated]"
	}
	return s
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	return clip(s, n)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
