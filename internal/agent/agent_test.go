package agent

import (
	"reflect"
	"strings"
	"testing"
)

func TestClaudeParse(t *testing.T) {
	c := Claude{}
	cases := []struct {
		line string
		want []Event
	}{
		{`{"type":"system","subtype":"init","session_id":"s1","tools":[]}`, []Event{{Kind: KindInit, SessionID: "s1"}}},
		{`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/x/a.txt"}}]}}`,
			[]Event{{Kind: KindToolStart, Tool: "Read", Text: "/x/a.txt"}}},
		{`{"type":"user","message":{"content":[{"tool_use_id":"t1","type":"tool_result","content":"boom","is_error":true}]}}`,
			[]Event{{Kind: KindToolEnd, IsError: true, Text: "boom"}}},
		{`{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}`, []Event{{Kind: KindText, Text: "hello"}}},
		{`{"type":"result","subtype":"success","is_error":false,"result":"hello","session_id":"s1","total_cost_usd":0.5}`,
			[]Event{{Kind: KindResult, Text: "hello", SessionID: "s1", Cost: 0.5}}},
		{`{"type":"rate_limit_event"}`, nil},
	}
	for _, tc := range cases {
		assertEvents(t, tc.line, c.Parse([]byte(tc.line)), tc.want)
	}
}

func TestCursorParse(t *testing.T) {
	c := Cursor{}
	cases := []struct {
		line string
		want []Event
	}{
		{`{"type":"system","subtype":"init","session_id":"c1","model":"Auto"}`, []Event{{Kind: KindInit, SessionID: "c1"}}},
		{`{"type":"thinking","subtype":"delta","text":"hmm","session_id":"c1"}`, []Event{{Kind: KindThinking, Text: "hmm", Delta: true}}},
		{`{"type":"tool_call","subtype":"started","tool_call":{"readToolCall":{"args":{"path":"/x/a.txt"}},"toolCallId":"1"}}`,
			[]Event{{Kind: KindToolStart, Tool: "Read", Text: "/x/a.txt"}}},
		{`{"type":"tool_call","subtype":"completed","tool_call":{"readToolCall":{"args":{"path":"/x"},"result":{"success":{"content":"hi"}}}}}`,
			[]Event{{Kind: KindToolEnd, Tool: "Read"}}},
		{`{"type":"tool_call","subtype":"completed","tool_call":{"shellToolCall":{"args":{"command":"ls"},"result":{"rejected":{}}}}}`,
			[]Event{{Kind: KindToolEnd, Tool: "Shell", IsError: true, Text: "rejected: {}"}}},
		{`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]},"session_id":"c1"}`,
			[]Event{{Kind: KindText, Text: "hello"}}},
		{`{"type":"result","subtype":"success","is_error":false,"result":"hello","session_id":"c1"}`,
			[]Event{{Kind: KindResult, Text: "hello", SessionID: "c1"}}},
	}
	for _, tc := range cases {
		assertEvents(t, tc.line, c.Parse([]byte(tc.line)), tc.want)
	}
}

func TestArgsEndWithPrompt(t *testing.T) {
	for _, d := range []Driver{Claude{}, Cursor{}} {
		args := d.Args(Spec{Prompt: "-do it", Approval: ApprovalYolo, ResumeID: "r1", AddDirs: []string{"/notes"}})
		if !strings.Contains(strings.Join(args, " "), "--add-dir /notes") {
			t.Errorf("%s: missing --add-dir, got %v", d.Name(), args)
		}
		if n := len(args); n < 2 || args[n-2] != "--" || args[n-1] != "-do it" {
			t.Errorf("%s: prompt must follow --, got %v", d.Name(), args)
		}
	}
}

func assertEvents(t *testing.T, line string, got, want []Event) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s\n got %d events %+v, want %d", line, len(got), got, len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("%s\n event %d: got %+v, want %+v", line, i, got[i], want[i])
		}
	}
}

func TestRateLimitAndModels(t *testing.T) {
	line := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1795297200,"rateLimitType":"five_hour","unifiedWindows":{"seven_day":{"utilization":0.02,"resetsAt":1795597200,"status":"allowed"},"five_hour":{"utilization":0.41,"resetsAt":1795297200,"status":"allowed"}}}}`
	evs := Claude{}.Parse([]byte(line))
	if len(evs) != 1 || evs[0].Kind != KindLimits || len(evs[0].Limits) != 2 {
		t.Fatalf("events = %+v", evs)
	}
	if evs[0].Limits[0].Window != "five_hour" || evs[0].Limits[0].Utilization != 0.41 || evs[0].Limits[1].Window != "seven_day" {
		t.Fatalf("limits = %+v", evs[0].Limits)
	}
	out := "Available models\n\nauto - Auto (default)\ngpt-5.3-codex - Codex 5.3\nclaude-opus-5-5-high - Claude Opus 5.5 1M High\ngrok-4.7-low-fast - Grok 4.7  Low Fast​​\nmuse-spark-1.3 - Muse Spark 1.3\nglm-5.2 - GLM 5.2\nweird-model - Weird\n"
	ms := ParseCursorModels(out)
	want := map[string]string{"auto": "Cursor", "gpt-5.3-codex": "OpenAI", "claude-opus-5-5-high": "Anthropic", "grok-4.7-low-fast": "xAI", "muse-spark-1.3": "Meta", "glm-5.2": "Z.ai", "weird-model": "unknown"}
	if len(ms) != len(want) || ms[0].ID != "auto" {
		t.Fatalf("models = %+v", ms)
	}
	for _, m := range ms {
		if want[m.ID] != m.Provider {
			t.Errorf("%s provider = %s, want %s", m.ID, m.Provider, want[m.ID])
		}
		if m.ID == "grok-4.7-low-fast" && m.Name != "Grok 4.7 Low Fast" {
			t.Errorf("name not cleaned: %q", m.Name)
		}
	}
	if WindowLabel("five_hour") != "5h" || WindowLabel("seven_day_opus") != "7d opus" {
		t.Fatal("window labels")
	}
}

func TestConfirmModeWiresPermitTool(t *testing.T) {
	args := Claude{}.Args(Spec{Prompt: "p", Approval: ApprovalConfirm, Permit: []string{"/bin/seraph", "permit", "abc"}})
	joined := strings.Join(args, " ")
	for _, want := range []string{"--permission-prompt-tool " + PermitTool, `"command":"/bin/seraph"`, `"args":["permit","abc"]`, "acceptEdits", "Bash(go test:*)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args lack %q: %s", want, joined)
		}
	}
	if args[len(args)-1] != "p" || args[len(args)-2] != "--" {
		t.Fatal("prompt must stay last")
	}
	if strings.Contains(strings.Join(Cursor{}.Args(Spec{Prompt: "p", Approval: ApprovalConfirm}), " "), "--force") {
		t.Fatal("cursor confirm must not force")
	}
}

func TestTrustedMCPTools(t *testing.T) {
	for _, a := range []Approval{ApprovalSafe, ApprovalTest, ApprovalConfirm} {
		joined := strings.Join(Claude{}.Args(Spec{Prompt: "p", Approval: a, AllowTools: []string{"mcp__penpot"}}), " ")
		if !strings.Contains(joined, "mcp__penpot") {
			t.Errorf("%s must allow trusted MCP servers: %s", a, joined)
		}
	}
	for _, a := range []Approval{ApprovalReadOnly, ApprovalCheck, ApprovalAsk} {
		if strings.Contains(strings.Join(Claude{}.Args(Spec{Prompt: "p", Approval: a, AllowTools: []string{"mcp__penpot"}}), " "), "mcp__penpot") {
			t.Errorf("%s is read-only and must not get MCP write tools", a)
		}
	}
}
