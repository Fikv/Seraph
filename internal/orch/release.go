package orch

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"seraph/internal/agent"
	"seraph/internal/store"
	"seraph/internal/workspace"
)

const roleValidatorSession = "validator"

const validatorPrompt = `This work is about to be merged into %s and pushed. Validate it with fresh eyes.

## What the user asked for
%s

## What the worker says it did
%s

## The change (git diff %s)
%s

Work in this directory (branch %s). You may read anything and run read-only and check commands: builds, tests, linters, type checks. Never edit, create or delete files, and never commit, push or merge.
Check:
1. Every explicit ask in the request: done, not done, or "release step" for merging, pushing or publishing (Seraph does those after you; never fail for them).
2. The project still builds and its tests pass. Find the commands (package.json, Makefile, go.mod, pyproject, README) and run the relevant ones.
3. No unrelated or unrequested changes, debug leftovers, broken docs, or secrets and credentials in the diff.

Reply in markdown:
## Requests
A table: ask | status | evidence.
## Checks run
One line per command: command - result.
## Problems
Each tagged [blocking] or [minor], with path:line.
Then end with exactly one line: VERDICT: PASS or VERDICT: FAIL. FAIL only when there is a [blocking] problem.`

const findingsPrompt = `An independent validator checked your work before release and found problems. Fix every [blocking] one; take [minor] ones when cheap. If a finding is wrong, say why.

Validator report:

%s

End with a short report of what you changed.`

type ReleaseTarget struct {
	Kind      string
	ID        string
	Title     string
	Repo      string
	Dir       string
	Branch    string
	Base      string
	ProjectID string
	Request   string
	Report    string
}

func (m *Manager) ReleaseTarget(kind, id string) (ReleaseTarget, error) {
	switch kind {
	case store.TargetSession:
		s, ok := m.Get(id)
		if !ok {
			return ReleaseTarget{}, fmt.Errorf("no session %s", id)
		}
		if !s.Worktree || s.Repo == "" || s.Branch == "" {
			return ReleaseTarget{}, errors.New("this worker shares a directory, so there is no branch to release")
		}
		if m.Active(id) {
			return ReleaseTarget{}, errors.New("the worker is still running")
		}
		var asks []string
		evs, _ := m.Log(id)
		for _, ev := range evs {
			if ev.Kind == agent.KindUser {
				asks = append(asks, strings.TrimSpace(ev.Text))
			}
		}
		if len(asks) == 0 {
			asks = []string{s.Task}
		}
		return ReleaseTarget{Kind: kind, ID: id, Title: s.Title, Repo: s.Repo, Dir: s.Dir, Branch: s.Branch, Base: s.Base,
			ProjectID: s.ProjectID, Request: strings.Join(asks, "\n\n---\n\n"), Report: m.resultText(id)}, nil
	case store.TargetMission:
		ms, nodes, err := m.Mission(id)
		if err != nil {
			return ReleaseTarget{}, err
		}
		if ms.State != store.MissionDone {
			return ReleaseTarget{}, errors.New("the mission must be accepted first")
		}
		v := &missionView{ms: ms, nodes: nodes}
		request, report := ms.Goal, ""
		if po := v.last(store.RolePO); po != nil {
			request += "\n\nSpecification:\n\n" + readFile(po.Artifact, inlineLimit)
		}
		if acc := v.last(store.RoleAcceptance); acc != nil {
			report = readFile(acc.Artifact, inlineLimit)
		}
		return ReleaseTarget{Kind: kind, ID: id, Title: ms.Title, Repo: ms.Repo, Dir: ms.Dir, Branch: ms.Branch, Base: ms.Base,
			ProjectID: ms.ProjectID, Request: request, Report: report}, nil
	}
	return ReleaseTarget{}, fmt.Errorf("unknown release kind %q", kind)
}

func (m *Manager) Release(kind, id string) (store.Release, bool) {
	r, ok, _ := m.st.LatestRelease(kind, id)
	return r, ok
}

func (m *Manager) Validate(kind, id string) (store.Release, error) {
	tg, err := m.ReleaseTarget(kind, id)
	if err != nil {
		return store.Release{}, err
	}
	if prev, ok := m.Release(kind, id); ok && prev.Status == store.ReleaseValidating && m.Active(prev.SessionID) {
		return prev, errors.New("a validation is already running")
	}
	if dirty, err := workspace.Dirty(tg.Dir); err != nil {
		return store.Release{}, fmt.Errorf("workspace is gone: %s", tg.Dir)
	} else if dirty != "" {
		return store.Release{}, errors.New("the worktree has uncommitted changes: let the worker finish or commit them first")
	}
	head, err := workspace.Head(tg.Repo, tg.Branch)
	if err != nil {
		return store.Release{}, err
	}
	cfg := m.Role(store.RoleValidator)
	rng := tg.Base + "..." + tg.Branch
	prompt := fmt.Sprintf(validatorPrompt, tg.Base, tg.Request, orDash(tg.Report), rng,
		workspace.DiffText(tg.Repo, rng, diffLimit), tg.Branch)
	sid, err := m.Spawn(SpawnOpts{
		Title: "Validator: " + tg.Title, Task: prompt, Agent: cfg.Agent, Model: cfg.Model, Approval: cfg.Approval,
		Shared: true, Repo: tg.Repo, Dir: tg.Dir, Branch: tg.Branch, Base: tg.Base, ProjectID: tg.ProjectID, Role: roleValidatorSession,
	})
	if err != nil {
		return store.Release{}, err
	}
	r := store.Release{ID: store.NewID(), Kind: kind, TargetID: id, Title: tg.Title, Repo: tg.Repo, Branch: tg.Branch,
		Base: tg.Base, Head: head, SessionID: sid, Status: store.ReleaseValidating}
	if err := m.st.SaveRelease(r); err != nil {
		return r, err
	}
	m.publish("release:"+id, true)
	return r, nil
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(no report)"
	}
	return s
}

func (m *Manager) validationEnded(s store.Session) {
	r, ok, _ := m.st.ReleaseBySession(s.ID)
	if !ok || r.Status != store.ReleaseValidating {
		return
	}
	text := m.resultText(s.ID)
	switch {
	case s.Status != store.StatusDone:
		r.Status, r.Note = store.ReleaseError, "validator "+firstNonEmpty(s.Error, string(s.Status))
	case strings.TrimSpace(text) == "":
		r.Status, r.Note = store.ReleaseError, "the validator finished without a report"
	default:
		r.Report = text
		r.Status = store.ReleaseFail
		if parseVerdict(text) == "PASS" {
			r.Status = store.ReleasePass
		}
	}
	if dirty, err := workspace.Dirty(s.Dir); err == nil && dirty != "" {
		_ = workspace.Stash(s.Dir, "seraph: files the validator changed")
		r.Note = strings.TrimSpace(r.Note + " the validator changed files; they were stashed (git stash list) and are not part of the release")
	}
	_ = m.st.SaveRelease(r)
	m.publish("release:"+r.TargetID, true)
}

func (m *Manager) Merge(kind, id string) (store.Release, error) {
	r, ok := m.Release(kind, id)
	if !ok || r.Status != store.ReleasePass {
		return r, errors.New("validate first: only a PASS can be merged")
	}
	if !r.MergedAt.IsZero() {
		return r, errors.New("already merged")
	}
	head, err := workspace.Head(r.Repo, r.Branch)
	if err != nil {
		return r, err
	}
	if head != r.Head {
		return r, errors.New("the branch changed after validation: validate again")
	}
	sha, err := workspace.MergeInto(r.Repo, r.Base, r.Branch, "Merge "+r.Title)
	if err != nil {
		return r, err
	}
	r.MergedAt, r.MergeSHA = time.Now(), sha
	err = m.st.SaveRelease(r)
	m.publish("release:"+id, true)
	return r, err
}

func (m *Manager) Push(kind, id string) (store.Release, error) {
	r, ok := m.Release(kind, id)
	if !ok || r.MergedAt.IsZero() {
		return r, errors.New("merge first")
	}
	if !r.PushedAt.IsZero() {
		return r, errors.New("already pushed")
	}
	out, err := workspace.Push(r.Repo, r.Base)
	r.PushOut = out
	if err != nil {
		r.Note = strings.TrimSpace(r.Note + " push failed: " + err.Error())
		_ = m.st.SaveRelease(r)
		return r, err
	}
	r.PushedAt = time.Now()
	err = m.st.SaveRelease(r)
	m.publish("release:"+id, true)
	return r, err
}

func (m *Manager) SendFindings(kind, id string) error {
	if kind != store.TargetSession {
		return errors.New("send findings to a single worker; for a mission start a follow-up mission")
	}
	r, ok := m.Release(kind, id)
	if !ok || r.Status != store.ReleaseFail {
		return errors.New("there is no failed validation to send")
	}
	return m.Followup(id, fmt.Sprintf(findingsPrompt, r.Report))
}

func (m *Manager) tickReleases() {
	rs, err := m.st.Releases(store.ReleaseValidating)
	if err != nil {
		return
	}
	for _, r := range rs {
		s, ok := m.Get(r.SessionID)
		switch {
		case !ok && time.Since(r.UpdatedAt) > staleAfter:
			r.Status, r.Note = store.ReleaseError, "validator session disappeared"
			_ = m.st.SaveRelease(r)
		case ok && !m.Active(s.ID) && s.Status != store.StatusWorking && s.Status != store.StatusQueued:
			m.validationEnded(s)
		}
	}
}

func RunTarget(st *store.Store, cwdRepo string) (repo, projectID string) {
	id := st.Setting(store.CurrentProjectSetting)
	if id == "" {
		return cwdRepo, ""
	}
	p, ok, _ := st.Project(id)
	if !ok || len(p.Repos) == 0 {
		return cwdRepo, ""
	}
	if r := ProjectRepo(p, cwdRepo); r != "" {
		return r, p.ID
	}
	return cwdRepo, p.ID
}
