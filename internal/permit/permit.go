package permit

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"seraph/internal/store"
)

const (
	ServerName = "seraph_permit"
	ToolName   = "approve"
	ToolRef    = "mcp__" + ServerName + "__" + ToolName
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type decision struct {
	Behavior     string         `json:"behavior"`
	UpdatedInput map[string]any `json:"updatedInput,omitempty"`
	Message      string         `json:"message,omitempty"`
}

type Server struct {
	Store     *store.Store
	SessionID string
	Poll      time.Duration
	Timeout   time.Duration
}

func (s Server) Serve(r io.Reader, w io.Writer) error {
	if s.Poll == 0 {
		s.Poll = 250 * time.Millisecond
	}
	if s.Timeout == 0 {
		s.Timeout = 30 * time.Minute
	}
	enc := json.NewEncoder(w)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var req request
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
			if p.ProtocolVersion == "" {
				p.ProtocolVersion = "2025-06-18"
			}
			result = map[string]any{
				"protocolVersion": p.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": ServerName, "version": "1"},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{
				"name":        ToolName,
				"description": "Asks the Seraph user whether the agent may use a tool.",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tool_name":   map[string]any{"type": "string"},
						"input":       map[string]any{"type": "object"},
						"tool_use_id": map[string]any{"type": "string"},
					},
					"required": []string{"tool_name", "input"},
				},
			}}}
		case "tools/call":
			var p struct {
				Arguments struct {
					ToolName string         `json:"tool_name"`
					Input    map[string]any `json:"input"`
				} `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			d := s.decide(p.Arguments.ToolName, p.Arguments.Input)
			text, _ := json.Marshal(d)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(text)}}}
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

func (s Server) decide(tool string, input map[string]any) decision {
	if input == nil {
		input = map[string]any{}
	}
	allow := decision{Behavior: "allow", UpdatedInput: input}
	if rules, err := s.Store.PermitRules(s.SessionID); err == nil && store.RuleAllows(rules, tool, input) {
		return allow
	}
	id := store.NewID() + store.NewID()
	if err := s.Store.AddPermission(store.Permission{ID: id, SessionID: s.SessionID, Tool: tool, Input: input}); err != nil {
		return decision{Behavior: "deny", Message: "Seraph could not record the permission request: " + err.Error()}
	}
	deadline := time.Now().Add(s.Timeout)
	for time.Now().Before(deadline) {
		p, ok, err := s.Store.Permission(id)
		if err == nil && ok {
			switch p.Status {
			case store.PermAllow:
				return allow
			case store.PermDeny, store.PermExpired:
				msg := p.Message
				if msg == "" {
					msg = "The user denied this. Do not try to work around it; continue without it or explain what you need."
				}
				return decision{Behavior: "deny", Message: msg}
			}
		}
		time.Sleep(s.Poll)
	}
	_, _ = s.Store.DecidePermission(id, store.PermExpired, "no answer")
	return decision{Behavior: "deny", Message: fmt.Sprintf("Nobody answered within %s, so this was denied.", s.Timeout)}
}
