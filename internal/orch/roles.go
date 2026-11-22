package orch

import (
	"fmt"
	"strings"

	"seraph/internal/store"
	"seraph/internal/workspace"
)

const poPrompt = `Mission goal:

%s

Read the codebase as much as you need to understand the current behaviour (read-only).
Write the specification as markdown with exactly these sections:

# <short title>
## Goal
What the user gets and why, in a few sentences.
## User stories
## Acceptance criteria
Numbered AC1, AC2, ... Each one concrete and testable by running the software or its tests.
## Out of scope
## Analysis questions
0-4 bullet questions about the EXISTING code that analysts must answer before anyone can plan. Ask only what the plan depends on. Write "- none" when the change is small and clear.

Do not design the implementation and do not write code. Reply with the specification only.`

const analystPrompt = `Question for you:

%s

The specification (for context only) is in %s:

%s

Investigate the codebase read-only and answer only this question; other analysts cover the other questions.
Reply in markdown:
## Answer
## Relevant code
One line per file or symbol: path:line - why it matters.
## Risks and constraints
No implementation plan and no code changes.`

const leadPrompt = `Inputs (also in %s):

### Specification
%s
%s
Split the work into 1-5 developer tasks. Developers work in parallel, each in their own git worktree branched from the mission branch, and cannot see each other's work until it is merged.
Rules:
- Every task owns a set of paths (files or globs such as "internal/api/**"). Tasks that run in parallel must own disjoint paths. If two tasks must touch the same file, make one depend on the other.
- Use depends_on when a task needs another task's result; that task then starts after the other is merged.
- Prefer fewer, larger tasks. A small change is a single task. Do not create separate tasks just for tests: each developer tests their own change.
- Each task text is the developer's whole brief: what to build, which acceptance criteria it covers, where in the code, and what not to touch.

Reply in markdown:
## Approach
## Tasks
A short paragraph per task.
## Risks

End with the task list as a fenced json block, exactly this shape:
` + "```json" + `
[{"id": "t1", "title": "short title", "task": "complete brief", "files": ["path/or/glob/**"], "depends_on": []}]
` + "```"

const devPrompt = `Task: %s

%s

Paths you own: %s
Change only these paths. If the task needs a change elsewhere, do not make it: describe it in your report so the tech lead can handle it.

The specification is in %s:

%s

Add or update tests for your change where the project has tests, and run the relevant checks when you can.
End with a report:
## Done
## Acceptance criteria covered
## Not done / needs someone else`

const reviewPrompt = `Review the change a developer made for this task.

Task: %s

%s

Paths the developer owns: %s

Specification:

%s

The change (git diff %s):

%s

Read the surrounding code as needed. Check correctness, the acceptance criteria this task covers, edits outside the owned paths, missing or weak tests, error handling and security. Do not modify anything.
Reply in markdown:
## Findings
Each finding tagged [blocking] or [minor], with path:line and a concrete fix.
## Summary
Then end with exactly one line: VERDICT: APPROVE or VERDICT: CHANGES. Use CHANGES only when there is at least one [blocking] finding.`

const testerPrompt = `You work on the mission branch %s, where all developer tasks are merged.

Specification:

%s

1. Find how this project runs its tests.
2. Write or extend tests that check the acceptance criteria. Change only test files and test data; never product code.
3. Run the full relevant test suite.
Seraph commits your tests; do not commit yourself.
Reply in markdown:
## Acceptance criteria
One line per criterion: AC id - covered by which test - pass/fail.
## Test run
The commands you ran and the result.
## Failures
For each failure: what fails, the output, and your best guess at the cause.
Then end with exactly one line: VERDICT: PASS, VERDICT: FAIL, or VERDICT: UNTESTED when you could not run the tests (say why).`

const acceptancePrompt = `The developers' work is merged on branch %s and tested. Judge it against your specification.

Specification:

%s

Latest test report:

%s

The full change against %s:

%s

Read the code where the diff is not enough. Do not modify anything.
Reply in markdown:
## Acceptance criteria
A table: AC | met? | evidence (file, test or reasoning).
## Gaps
## Recommendation
Then end with exactly one line: VERDICT: ACCEPT or VERDICT: REJECT.`

func (m *Manager) nodePrompt(v *missionView, n *store.Node) (string, error) {
	ms := v.ms
	spec := ""
	specPath := ""
	if po := v.last(store.RolePO); po != nil && po.Artifact != "" {
		specPath = po.Artifact
		spec = readFile(po.Artifact, inlineLimit)
	}
	switch n.Role {
	case store.RolePO:
		return fmt.Sprintf(poPrompt, ms.Goal), nil
	case store.RoleAnalyst:
		return fmt.Sprintf(analystPrompt, n.Task, specPath, spec), nil
	case store.RoleLead:
		var analyses strings.Builder
		for _, a := range v.byRole(store.RoleAnalyst, "*") {
			if a.Artifact != "" {
				fmt.Fprintf(&analyses, "\n### Analysis: %s\n%s\n", a.Title, readFile(a.Artifact, inlineLimit))
			}
		}
		return fmt.Sprintf(leadPrompt, ms.Artifacts, spec, analyses.String()), nil
	case store.RoleDev:
		files := strings.Join(n.Files, ", ")
		if n.ParentID != "" {
			files = "any path needed for the fix; keep the change minimal"
		}
		if files == "" {
			files = "(none listed: keep the change minimal and focused on the task)"
		}
		return fmt.Sprintf(devPrompt, n.Title, n.Task, files, specPath, spec), nil
	case store.RoleReviewer:
		dev := v.node(n.ParentID)
		if dev == nil {
			return "", fmt.Errorf("reviewer %q has no developer", n.Title)
		}
		sess, ok := m.Get(dev.SessionID)
		if !ok {
			return "", fmt.Errorf("developer session for %q is gone", dev.Title)
		}
		rng := ms.Branch + "..." + sess.Branch
		return fmt.Sprintf(reviewPrompt, dev.Title, dev.Task, strings.Join(dev.Files, ", "), spec, rng,
			workspace.DiffText(ms.Dir, rng, diffLimit)), nil
	case store.RoleTester:
		return fmt.Sprintf(testerPrompt, ms.Branch, spec), nil
	case store.RoleAcceptance:
		report := "(no test report)"
		if t := v.last(store.RoleTester); t != nil && t.Artifact != "" {
			report = readFile(t.Artifact, inlineLimit)
		}
		rng := ms.Base + "..." + ms.Branch
		return fmt.Sprintf(acceptancePrompt, ms.Branch, spec, report, ms.Base, workspace.DiffText(ms.Dir, rng, diffLimit)), nil
	}
	return "", fmt.Errorf("no prompt for role %s", n.Role)
}
