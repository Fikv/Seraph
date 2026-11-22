package agent

import (
	"context"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type Model struct {
	Agent    string `json:"agent"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

var claudeModels = []Model{
	{Agent: "claude", ID: "", Name: "Claude Code default", Provider: "Anthropic"},
	{Agent: "claude", ID: "claude-fable-5-2", Name: "Claude Fable 5.2", Provider: "Anthropic"},
	{Agent: "claude", ID: "claude-opus-5-5", Name: "Claude Opus 5.5", Provider: "Anthropic"},
	{Agent: "claude", ID: "claude-sonnet-5-5", Name: "Claude Sonnet 5.5", Provider: "Anthropic"},
	{Agent: "claude", ID: "claude-haiku-5-5", Name: "Claude Haiku 5.5", Provider: "Anthropic"},
	{Agent: "claude", ID: "claude-opus-5", Name: "Claude Opus 5", Provider: "Anthropic"},
	{Agent: "claude", ID: "claude-sonnet-5", Name: "Claude Sonnet 5", Provider: "Anthropic"},
	{Agent: "claude", ID: "claude-opus-4-8", Name: "Claude Opus 4.8", Provider: "Anthropic"},
}

func ClaudeModels() []Model { return append([]Model(nil), claudeModels...) }

var providerPrefixes = []struct{ prefix, provider string }{
	{"claude", "Anthropic"},
	{"gpt", "OpenAI"},
	{"o3", "OpenAI"},
	{"o4", "OpenAI"},
	{"codex", "OpenAI"},
	{"gemini", "Google"},
	{"cursor-grok", "xAI"},
	{"grok", "xAI"},
	{"deepseek", "DeepSeek"},
	{"glm", "Z.ai"},
	{"kimi", "Moonshot AI"},
	{"muse", "Meta"},
	{"qwen", "Alibaba"},
	{"composer", "Cursor"},
	{"auto", "Cursor"},
	{"cursor", "Cursor"},
}

func ProviderOf(id string) string {
	id = strings.ToLower(id)
	for _, p := range providerPrefixes {
		if strings.HasPrefix(id, p.prefix) {
			return p.provider
		}
	}
	return "unknown"
}

func ParseCursorModels(out string) []Model {
	var models []Model
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		id, name, ok := strings.Cut(strings.TrimSpace(line), " - ")
		if !ok || id == "" || strings.ContainsAny(id, " \t") || seen[id] {
			continue
		}
		seen[id] = true
		name = strings.TrimSpace(strings.Map(func(r rune) rune {
			if r == '​' || r == '‌' || r == '‍' {
				return -1
			}
			return r
		}, name))
		name = strings.Join(strings.Fields(name), " ")
		models = append(models, Model{Agent: "cursor", ID: id, Name: name, Provider: ProviderOf(id)})
	}
	sort.SliceStable(models, func(i, j int) bool {
		if (models[i].ID == "auto") != (models[j].ID == "auto") {
			return models[i].ID == "auto"
		}
		return false
	})
	return models
}

func CursorModels(ctx context.Context) ([]Model, error) {
	bin, err := Locate(Cursor{})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--list-models").CombinedOutput()
	if err != nil {
		return nil, err
	}
	return ParseCursorModels(string(out)), nil
}

func ModelName(agentName, id string, catalog []Model) string {
	for _, m := range catalog {
		if m.Agent == agentName && m.ID == id {
			return m.Name
		}
	}
	if id == "" {
		return "default"
	}
	return id
}

type Limit struct {
	Window      string    `json:"window"`
	Utilization float64   `json:"utilization"`
	ResetsAt    time.Time `json:"resets_at"`
	Status      string    `json:"status"`
}

type Limits struct {
	Agent    string    `json:"agent"`
	Windows  []Limit   `json:"windows"`
	Captured time.Time `json:"captured"`
}

var windowOrder = map[string]int{"five_hour": 0, "seven_day": 1}

func SortLimits(ls []Limit) {
	sort.SliceStable(ls, func(i, j int) bool {
		a, aok := windowOrder[ls[i].Window]
		b, bok := windowOrder[ls[j].Window]
		switch {
		case aok && bok:
			return a < b
		case aok != bok:
			return aok
		}
		return ls[i].Window < ls[j].Window
	})
}

func WindowLabel(w string) string {
	switch w {
	case "five_hour":
		return "5h"
	case "seven_day":
		return "7d"
	}
	w = strings.ReplaceAll(w, "seven_day_", "7d ")
	w = strings.ReplaceAll(w, "five_hour_", "5h ")
	return strings.ReplaceAll(w, "_", " ")
}
