package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"seraph/internal/agent"
)

func open(t *testing.T) *Store {
	t.Helper()
	t.Setenv("SERAPH_HOME", t.TempDir())
	st, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestSessionsAndEvents(t *testing.T) {
	st := open(t)
	now := time.Now().Truncate(time.Millisecond)
	s := Session{ID: "abc123", Title: "t", Task: "task", Agent: "claude", Approval: "safe", Dir: "/x",
		Status: StatusWorking, Worktree: true, Cost: 0.5, Turns: 2, CreatedAt: now, UpdatedAt: now, HeartbeatAt: now, OwnerPID: 42}
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	got, ok, err := st.Get("abc123")
	if err != nil || !ok {
		t.Fatalf("get: %v %v", ok, err)
	}
	if got.Title != "t" || !got.Worktree || got.Cost != 0.5 || got.OwnerPID != 42 || !got.CreatedAt.Equal(now) || got.Status != StatusWorking {
		t.Fatalf("roundtrip = %+v", got)
	}
	s.Status, s.LastLine = StatusDone, "done"
	if err := st.Save(s); err != nil {
		t.Fatal(err)
	}
	list, _ := st.List()
	if len(list) != 1 || list[0].Status != StatusDone || list[0].LastLine != "done" {
		t.Fatalf("list = %+v", list)
	}

	for i, k := range []agent.Kind{agent.KindUser, agent.KindText, agent.KindResult} {
		if _, err := st.AppendLog("abc123", agent.Event{Kind: k, Text: string(k), Cost: float64(i), IsError: i == 2, Time: now}); err != nil {
			t.Fatal(err)
		}
	}
	evs, last, err := st.ReadLogSince("abc123", 0)
	if err != nil || len(evs) != 3 || evs[2].Kind != agent.KindResult || !evs[2].IsError || evs[2].Cost != 2 {
		t.Fatalf("events = %+v %v", evs, err)
	}
	st.AppendLog("abc123", agent.Event{Kind: agent.KindText, Text: "more", Time: now})
	more, _, _ := st.ReadLogSince("abc123", last)
	if len(more) != 1 || more[0].Text != "more" {
		t.Fatalf("since = %+v", more)
	}

	if err := st.Delete("abc123"); err != nil {
		t.Fatal(err)
	}
	if evs, _ := st.ReadLog("abc123"); len(evs) != 0 {
		t.Fatalf("events not cascaded: %d", len(evs))
	}
}

func TestHeartbeatAndStopRequests(t *testing.T) {
	st := open(t)
	for _, id := range []string{"a", "b"} {
		st.Save(Session{ID: id, Title: id, Task: id, Agent: "claude", Status: StatusWorking, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	}
	if err := st.RequestStop("b", true); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	stop, err := st.Heartbeat([]string{"a", "b"}, 777, now)
	if err != nil || len(stop) != 1 || stop[0] != "b" {
		t.Fatalf("stop = %v %v", stop, err)
	}
	a, _, _ := st.Get("a")
	if a.OwnerPID != 777 || a.HeartbeatAt.UnixMilli() != now.UnixMilli() {
		t.Fatalf("heartbeat not stored: %+v", a)
	}
	a.Title = "renamed"
	st.Save(a)
	if stop, _ := st.Heartbeat([]string{"b"}, 777, now); len(stop) != 1 {
		t.Fatal("Save must not clear stop_requested")
	}
}

func TestSettingsAndCache(t *testing.T) {
	st := open(t)
	if st.Setting("x") != "" {
		t.Fatal("unset setting should be empty")
	}
	st.SetSetting("x", "1")
	st.SetSetting("x", "2")
	if st.Setting("x") != "2" {
		t.Fatal("setting not updated")
	}
	st.SetSetting("x", "")
	if st.Setting("x") != "" {
		t.Fatal("setting not cleared")
	}
	ctx := context.Background()
	st.CacheSet(ctx, "k", "v", time.Hour)
	if v, ok := st.CacheGet(ctx, "k"); !ok || v != "v" {
		t.Fatal("cache miss")
	}
	st.CacheSet(ctx, "gone", "v", -time.Second)
	if _, ok := st.CacheGet(ctx, "gone"); ok {
		t.Fatal("expired entry returned")
	}
}

func TestLegacyImportAndReopen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SERAPH_HOME", home)
	dir := filepath.Join(home, "sessions")
	os.MkdirAll(dir, 0o755)
	data, _ := json.Marshal(Session{ID: "old111", Title: "old", Task: "old", Agent: "cursor", Status: StatusDone, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	os.WriteFile(filepath.Join(dir, "old111.json"), data, 0o644)
	var log strings.Builder
	for _, txt := range []string{"one", "two"} {
		b, _ := json.Marshal(agent.Event{Kind: agent.KindText, Text: txt, Time: time.Now()})
		log.Write(append(b, '\n'))
	}
	os.WriteFile(filepath.Join(dir, "old111.log"), []byte(log.String()), 0o644)

	st, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if s, ok, _ := st.Get("old111"); !ok || s.Agent != "cursor" {
		t.Fatalf("legacy session not imported: %+v", s)
	}
	if evs, _ := st.ReadLog("old111"); len(evs) != 2 || evs[1].Text != "two" {
		t.Fatalf("legacy log = %+v", evs)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("legacy dir should be renamed")
	}
	st.Close()

	st2, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if s := st2.Stats(); s.Sessions != 1 || s.Events != 2 {
		t.Fatalf("reopen stats = %+v", s)
	}
	if fi, _ := os.Stat(DBPath()); fi.Mode().Perm() != 0o600 {
		t.Fatalf("db perms = %v", fi.Mode().Perm())
	}
}
