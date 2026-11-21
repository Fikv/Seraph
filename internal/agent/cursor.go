package agent

import (
	"encoding/json"
	"strings"
)

type Cursor struct{}

func (Cursor) Name() string   { return "cursor" }
func (Cursor) Binary() string { return "cursor-agent" }

func (Cursor) Args(s Spec) []string {
	args := []string{"-p", "--output-format", "stream-json", "--trust"}
	if s.Dir != "" {
		args = append(args, "--workspace", s.Dir)
	}
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}
	if s.ResumeID != "" {
		args = append(args, "--resume", s.ResumeID)
	}
	if s.ApproveMCPs {
		args = append(args, "--approve-mcps")
	}
	switch s.Approval {
	case ApprovalYolo:
		args = append(args, "--force")
	case ApprovalReadOnly:
		args = append(args, "--mode", "plan")
	}
	prompt := s.Prompt
	if s.SystemPrompt != "" {
		prompt = s.SystemPrompt + "\n\n---\n\n" + prompt
	}
	return append(args, "--", prompt)
}

type cursorLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
	Message   struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
	ToolCall map[string]json.RawMessage `json:"tool_call"`
	Result   string                     `json:"result"`
	IsError  bool                       `json:"is_error"`
}

type cursorCall struct {
	Args   map[string]any             `json:"args"`
	Result map[string]json.RawMessage `json:"result"`
}

func (Cursor) Parse(line []byte) []Event {
	var l cursorLine
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	switch l.Type {
	case "system":
		if l.Subtype == "init" {
			return []Event{{Kind: KindInit, SessionID: l.SessionID}}
		}
	case "thinking":
		if l.Subtype == "delta" && l.Text != "" {
			return []Event{{Kind: KindThinking, Text: l.Text, Delta: true}}
		}
	case "assistant":
		var out []Event
		for _, c := range l.Message.Content {
			if c.Type == "text" && c.Text != "" {
				out = append(out, Event{Kind: KindText, Text: c.Text})
			}
		}
		return out
	case "tool_call":
		name, call := cursorTool(l.ToolCall)
		switch l.Subtype {
		case "started":
			return []Event{{Kind: KindToolStart, Tool: name, Text: summarize(call.Args)}}
		case "completed":
			ev := Event{Kind: KindToolEnd, Tool: name}
			if call.Result != nil {
				if _, ok := call.Result["success"]; !ok {
					ev.IsError = true
					for k, v := range call.Result {
						ev.Text = oneLine(k+": "+string(v), 160)
						break
					}
				}
			}
			return []Event{ev}
		}
	case "result":
		return []Event{{Kind: KindResult, Text: l.Result, IsError: l.IsError, SessionID: l.SessionID}}
	}
	return nil
}

func cursorTool(m map[string]json.RawMessage) (string, cursorCall) {
	var call cursorCall
	for k, v := range m {
		if !strings.HasSuffix(k, "ToolCall") {
			continue
		}
		_ = json.Unmarshal(v, &call)
		name := strings.TrimSuffix(k, "ToolCall")
		if name != "" {
			name = strings.ToUpper(name[:1]) + name[1:]
		}
		return name, call
	}
	return "Tool", call
}
