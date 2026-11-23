package team

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"seraph/internal/store"
)

func TestToolsDependOnDepth(t *testing.T) {
	t.Setenv("SERAPH_HOME", t.TempDir())
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	for _, s := range []store.Session{
		{ID: "o", TeamID: "o", Role: "orchestrator", Title: "Orchestrator: x", Task: "x", Agent: "claude", Status: store.StatusWorking},
		{ID: "a", TeamID: "o", ParentID: "o", AgentName: "api", Role: "subagent", Title: "Developer: api", Task: "x", Agent: "claude", Status: store.StatusDone},
		{ID: "h", TeamID: "o", ParentID: "a", AgentName: "helper", Role: "subagent", Title: "Helper: helper", Task: "x", Agent: "claude", Status: store.StatusDone},
	} {
		s.CreatedAt, s.UpdatedAt = now, now
		st.Save(s)
	}
	names := func(id string) string {
		inR, inW := io.Pipe()
		outR, outW := io.Pipe()
		go Server{Store: st, SessionID: id}.Serve(inR, outW)
		io.WriteString(inW, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`+"\n")
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		sc.Scan()
		inW.Close()
		var resp struct {
			Result struct {
				Tools []struct{ Name string } `json:"tools"`
			} `json:"result"`
		}
		json.Unmarshal(sc.Bytes(), &resp)
		var out []string
		for _, x := range resp.Result.Tools {
			out = append(out, x.Name)
		}
		return strings.Join(out, ",")
	}
	if o := names("o"); !strings.Contains(o, "start_mission") || !strings.Contains(o, "spawn_agent") {
		t.Fatalf("orchestrator tools = %s", o)
	}
	if a := names("a"); strings.Contains(a, "start_mission") || !strings.Contains(a, "spawn_agent") {
		t.Fatalf("depth-1 tools = %s", a)
	}
	if h := names("h"); strings.Contains(h, "spawn_agent") || !strings.Contains(h, "write_note") || !strings.Contains(h, "message") {
		t.Fatalf("depth-2 tools = %s", h)
	}
	if Depth(st, mustGet(st, "h")) != 2 {
		t.Fatal("depth")
	}
}

func mustGet(st *store.Store, id string) store.Session {
	s, _, _ := st.Get(id)
	return s
}
