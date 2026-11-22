package permit

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"seraph/internal/store"
)

func TestServerAsksAndRemembers(t *testing.T) {
	t.Setenv("SERAPH_HOME", t.TempDir())
	st, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go Server{Store: st, SessionID: "s1", Poll: 10 * time.Millisecond, Timeout: 5 * time.Second}.Serve(inR, outW)
	out := bufio.NewScanner(outR)
	call := func(id int, method, params string) map[string]any {
		t.Helper()
		io.WriteString(inW, `{"jsonrpc":"2.0","id":`+string(rune('0'+id))+`,"method":"`+method+`","params":`+params+"}\n")
		if !out.Scan() {
			t.Fatal("no reply")
		}
		var resp map[string]any
		json.Unmarshal(out.Bytes(), &resp)
		return resp
	}
	decisionOf := func(resp map[string]any) map[string]any {
		text := resp["result"].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
		var d map[string]any
		json.Unmarshal([]byte(text), &d)
		return d
	}
	if r := call(1, "initialize", `{"protocolVersion":"2025-06-18"}`); r["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("initialize = %v", r)
	}
	io.WriteString(inW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	if r := call(2, "tools/list", `{}`); !strings.Contains(mustJSON(r), `"approve"`) {
		t.Fatalf("tools = %v", r)
	}
	answer := func(want string, status store.PermStatus, rule bool) {
		for i := 0; i < 200; i++ {
			ps, _ := st.PendingPermissions()
			if len(ps) == 1 {
				if ps[0].Command() != want || ps[0].SessionID != "s1" {
					t.Errorf("request = %+v", ps[0])
				}
				if rule {
					st.AddPermitRule("s1", "Bash", store.CommandPrefix(ps[0].Command()))
				}
				st.DecidePermission(ps[0].ID, status, "")
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Error("no pending request")
	}
	push := `{"name":"approve","arguments":{"tool_name":"Bash","input":{"command":"git push origin main"}}}`
	go answer("git push origin main", store.PermDeny, false)
	if d := decisionOf(call(3, "tools/call", push)); d["behavior"] != "deny" || !strings.Contains(d["message"].(string), "denied") {
		t.Fatalf("deny = %v", d)
	}
	go answer("git push origin main", store.PermAllow, true)
	if d := decisionOf(call(4, "tools/call", push)); d["behavior"] != "allow" || d["updatedInput"].(map[string]any)["command"] != "git push origin main" {
		t.Fatalf("allow = %v", d)
	}
	if d := decisionOf(call(5, "tools/call", `{"name":"approve","arguments":{"tool_name":"Bash","input":{"command":"git push origin dev"}}}`)); d["behavior"] != "allow" {
		t.Fatalf("an always-allow rule must answer without asking: %v", d)
	}
	go answer("git push origin main && rm -rf /", store.PermDeny, false)
	if d := decisionOf(call(6, "tools/call", `{"name":"approve","arguments":{"tool_name":"Bash","input":{"command":"git push origin main && rm -rf /"}}}`)); d["behavior"] != "deny" {
		t.Fatalf("compound commands must always ask: %v", d)
	}
	inW.Close()
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestRuleAllows(t *testing.T) {
	rules := map[string][]string{"Bash": {"git push"}, "WebFetch": {""}}
	for cmd, want := range map[string]bool{"git push": true, "git push origin x": true, "git pushx": false, "git push; rm x": false, "git push | tee": false, "git status": false} {
		if got := store.RuleAllows(rules, "Bash", map[string]any{"command": cmd}); got != want {
			t.Errorf("%q = %v", cmd, got)
		}
	}
	if !store.RuleAllows(rules, "WebFetch", map[string]any{"url": "x"}) || store.RuleAllows(rules, "Write", nil) {
		t.Error("non-Bash rules")
	}
	if store.CommandPrefix("  git   push origin main") != "git push" || store.CommandPrefix("ls") != "ls" {
		t.Error("prefix")
	}
}
