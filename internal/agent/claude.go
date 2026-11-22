package agent

import (
	"encoding/json"
	"time"
)

type Claude struct{}

const noAttribution = `{"includeCoAuthoredBy":false,"attribution":{"commit":"","pr":""}}`

func (Claude) Name() string   { return "claude" }
func (Claude) Binary() string { return "claude" }

func (Claude) Args(s Spec) []string {
	args := []string{"-p", "--output-format", "stream-json", "--verbose"}
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}
	if s.ResumeID != "" {
		args = append(args, "--resume", s.ResumeID)
	}
	switch s.Approval {
	case ApprovalYolo:
		args = append(args, "--permission-mode", "bypassPermissions")
	case ApprovalReadOnly, ApprovalAsk:
		args = append(args, "--allowedTools", "Read,Grep,Glob")
	default:
		args = append(args, "--permission-mode", "acceptEdits")
	}
	for _, d := range s.AddDirs {
		args = append(args, "--add-dir", d)
	}
	args = append(args, "--settings", noAttribution)
	if s.SystemPrompt != "" {
		args = append(args, "--append-system-prompt", s.SystemPrompt)
	}
	return append(args, "--", s.Prompt)
}

type claudeBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Name     string          `json:"name"`
	Input    map[string]any  `json:"input"`
	IsError  bool            `json:"is_error"`
	Content  json.RawMessage `json:"content"`
}

type claudeLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	Message   struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Result    string  `json:"result"`
	IsError   bool    `json:"is_error"`
	TotalCost float64 `json:"total_cost_usd"`
	RateLimit struct {
		Status         string   `json:"status"`
		ResetsAt       int64    `json:"resetsAt"`
		RateLimitType  string   `json:"rateLimitType"`
		Utilization    *float64 `json:"utilization"`
		UnifiedWindows map[string]struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    int64   `json:"resetsAt"`
			Status      string  `json:"status"`
		} `json:"unifiedWindows"`
	} `json:"rate_limit_info"`
}

func (Claude) Parse(line []byte) []Event {
	var l claudeLine
	if json.Unmarshal(line, &l) != nil {
		return nil
	}
	switch l.Type {
	case "system":
		if l.Subtype == "init" {
			return []Event{{Kind: KindInit, SessionID: l.SessionID}}
		}
	case "assistant", "user":
		var blocks []claudeBlock
		if json.Unmarshal(l.Message.Content, &blocks) != nil {
			return nil
		}
		var out []Event
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if l.Type == "assistant" && b.Text != "" {
					out = append(out, Event{Kind: KindText, Text: b.Text})
				}
			case "thinking":
				if b.Thinking != "" {
					out = append(out, Event{Kind: KindThinking, Text: b.Thinking})
				}
			case "tool_use":
				out = append(out, Event{Kind: KindToolStart, Tool: b.Name, Text: summarize(b.Input)})
			case "tool_result":
				out = append(out, Event{Kind: KindToolEnd, IsError: b.IsError, Text: toolResultText(b)})
			}
		}
		return out
	case "result":
		return []Event{{Kind: KindResult, Text: l.Result, IsError: l.IsError, Cost: l.TotalCost, SessionID: l.SessionID}}
	case "rate_limit_event":
		var limits []Limit
		for w, v := range l.RateLimit.UnifiedWindows {
			limits = append(limits, Limit{Window: w, Utilization: v.Utilization, ResetsAt: unixTime(v.ResetsAt), Status: v.Status})
		}
		if len(limits) == 0 && l.RateLimit.RateLimitType != "" && l.RateLimit.Utilization != nil {
			limits = append(limits, Limit{Window: l.RateLimit.RateLimitType, Utilization: *l.RateLimit.Utilization, ResetsAt: unixTime(l.RateLimit.ResetsAt), Status: l.RateLimit.Status})
		}
		if len(limits) == 0 {
			return nil
		}
		SortLimits(limits)
		return []Event{{Kind: KindLimits, Limits: limits}}
	}
	return nil
}

func unixTime(s int64) time.Time {
	if s == 0 {
		return time.Time{}
	}
	return time.Unix(s, 0)
}

func toolResultText(b claudeBlock) string {
	if !b.IsError {
		return ""
	}
	var s string
	if json.Unmarshal(b.Content, &s) == nil {
		return oneLine(s, 160)
	}
	return ""
}
