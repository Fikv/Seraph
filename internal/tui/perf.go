package tui

import (
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"seraph/internal/agent"
	"seraph/internal/store"
)

type viewStats struct {
	at              time.Time
	limits          agent.Limits
	hasLimits       bool
	activeMissions  int
	waitingMissions int
}

func (m *Model) refreshStats() {
	s := viewStats{at: time.Now()}
	s.limits, s.hasLimits = m.mgr.Limits("claude")
	if all, err := m.mgr.Missions(); err == nil {
		for _, ms := range all {
			if m.project != nil && ms.ProjectID != m.project.ID {
				continue
			}
			if ms.Active() {
				s.activeMissions++
			}
			if ms.State == store.MissionWaiting || ms.State == store.MissionBlocked {
				s.waitingMissions++
			}
		}
	}
	m.stats = s
}

var (
	mouseSeq      = regexp.MustCompile(`\[?<\d+;\d+;\d+[Mm]`)
	mouseFragment = regexp.MustCompile(`^\[?<?[\d;]*[Mm]?$`)
)

const swallowFor = 60 * time.Millisecond

func (m *Model) mouseGarbage(k tea.KeyMsg) bool {
	s := k.String()
	raw := string(k.Runes)
	switch {
	case s == "alt+[" || s == "alt+<":
		m.swallowAt = time.Now()
		return true
	case k.Type != tea.KeyRunes || k.Paste:
		return false
	case mouseSeq.MatchString(raw), strings.Contains(raw, "[<"):
		m.swallowAt = time.Now()
		return true
	case len(k.Runes) > 1 && strings.ContainsAny(raw, "<;") && strings.ContainsAny(raw, "0123456789") && mouseFragment.MatchString(raw):
		m.swallowAt = time.Now()
		return true
	case time.Since(m.swallowAt) < swallowFor && mouseFragment.MatchString(raw):
		m.swallowAt = time.Now()
		return true
	}
	return false
}
