package main

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"seraph/internal/agent"
	t "seraph/internal/theme"
)

func cmdModels(args []string) error {
	q := strings.ToLower(strings.Join(args, " "))
	match := func(md agent.Model) bool {
		hay := strings.ToLower(md.Name + " " + md.ID + " " + md.Provider + " " + md.Agent)
		for _, w := range strings.Fields(q) {
			if !strings.Contains(hay, w) {
				return false
			}
		}
		return true
	}
	gold, dim, faint := t.FG(t.Gold).Bold(true), t.FG(t.Dim), t.FG(t.Faint)
	print := func(head string, ms []agent.Model) {
		fmt.Println(gold.Render(head))
		for _, md := range ms {
			if !match(md) {
				continue
			}
			id := md.ID
			if id == "" {
				id = "(no --model)"
			}
			fmt.Printf("  %-34s %-12s %s\n", md.Name, md.Provider, faint.Render(id))
		}
		fmt.Println()
	}
	print("✻ claude", agent.ClaudeModels())
	cms, err := agent.CursorModels(context.Background())
	if err != nil {
		fmt.Println(gold.Render("◆ cursor") + dim.Render("  unavailable: "+err.Error()) + "\n")
	} else {
		print("◆ cursor", cms)
	}
	mgr, stg, err := openManager()
	if err != nil {
		return err
	}
	defer func() { mgr.Close(); stg.close() }()
	l, ok := mgr.Limits("claude")
	if !ok {
		fmt.Println(dim.Render("limits: no data yet, they arrive with the next Claude worker run"))
		return nil
	}
	fmt.Println(gold.Render("limits") + faint.Render("  claude · captured "+l.Captured.Format("2006-01-02 15:04")))
	for _, w := range l.Windows {
		cells := int(math.Round(math.Min(math.Max(w.Utilization, 0), 1) * 20))
		reset := ""
		if !w.ResetsAt.IsZero() {
			reset = faint.Render("  resets " + w.ResetsAt.Local().Format("Mon 15:04") + " (in " + time.Until(w.ResetsAt).Round(time.Minute).String() + ")")
		}
		fmt.Printf("  %-8s %s%s %3d%%%s\n", agent.WindowLabel(w.Window), t.FG(t.Gold).Render(strings.Repeat("▰", cells)), faint.Render(strings.Repeat("▱", 20-cells)), int(math.Round(w.Utilization*100)), reset)
	}
	fmt.Println(dim.Render("  cursor: usage is not exposed by cursor-agent"))
	return nil
}
