package orch

import (
	"testing"
	"time"

	"seraph/internal/agent"
	"seraph/internal/store"
)

func TestParsePlan(t *testing.T) {
	tasks, err := parsePlan("Here you go:\n[{\"title\":\"A\",\"task\":\"do a\"},{\"title\":\"\",\"task\":\"do b\"},{\"title\":\"C\",\"task\":\" \"}]\nthanks")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].Title != "A" || tasks[1].Title != "do b" {
		t.Fatalf("got %+v", tasks)
	}
	if _, err := parsePlan("no json here"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := parsePlan("[]"); err == nil {
		t.Fatal("expected error for empty list")
	}
}

func TestLastNonEmpty(t *testing.T) {
	cases := map[string]string{
		"done\n\n```\ncode\n```\n": "code",
		"summary line\n---\n":      "summary line",
		"first\nsecond\n\n":        "second",
		"```\n```":                 "",
	}
	for in, want := range cases {
		if got := lastNonEmpty(in); got != want {
			t.Errorf("lastNonEmpty(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCrossProcessView(t *testing.T) {
	t.Setenv("SERAPH_HOME", t.TempDir())
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	m, err := New(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	now := time.Now()
	st.Save(store.Session{ID: "remote", Title: "r", Task: "r", Agent: "claude", Status: store.StatusWorking,
		OwnerPID: 99999, HeartbeatAt: now, CreatedAt: now, UpdatedAt: now})
	st.Save(store.Session{ID: "dead", Title: "d", Task: "d", Agent: "claude", Status: store.StatusWorking,
		OwnerPID: 99998, HeartbeatAt: now.Add(-time.Minute), CreatedAt: now, UpdatedAt: now})
	m.Refresh()

	if !m.RunningElsewhere("remote") || !m.Active("remote") || m.Running("remote") {
		t.Fatal("remote session should be active elsewhere")
	}
	if d, ok := m.Get("dead"); !ok || d.Status != store.StatusStopped {
		t.Fatalf("stale session should be stopped, got %+v", d)
	}
	if d, _, _ := st.Get("dead"); d.Status != store.StatusStopped {
		t.Fatal("stale fix must be persisted")
	}

	st.AppendLog("remote", agent.Event{Kind: agent.KindText, Text: "one", Time: now})
	evs, v1 := m.Log("remote")
	st.AppendLog("remote", agent.Event{Kind: agent.KindText, Text: "two", Time: now})
	evs, v2 := m.Log("remote")
	if len(evs) != 2 || evs[1].Text != "two" || v2 == v1 {
		t.Fatalf("incremental log = %+v (versions %d → %d)", evs, v1, v2)
	}
	if evs, _ = m.Log("remote"); len(evs) != 2 {
		t.Fatalf("log duplicated: %d", len(evs))
	}

	m.Stop("remote")
	if stop, _ := st.Heartbeat([]string{"remote"}, 99999, time.Now()); len(stop) != 1 {
		t.Fatal("stop request not recorded for the owning process")
	}
	if err := m.Remove("remote"); err == nil {
		t.Fatal("removing a session running elsewhere must fail")
	}
	if err := m.Followup("remote", "more"); err == nil {
		t.Fatal("follow-up on a session running elsewhere must fail")
	}

	st.Delete("dead")
	m.Refresh()
	if _, ok := m.Get("dead"); ok {
		t.Fatal("deleted session should disappear after refresh")
	}
}
