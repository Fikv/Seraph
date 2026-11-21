package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"seraph/internal/agent"
	"seraph/internal/logo"
	"seraph/internal/orch"
	"seraph/internal/store"
	t "seraph/internal/theme"
	"seraph/internal/tui"
	"seraph/internal/workspace"
)

const version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, t.FG(t.Rose).Render("✗ "+err.Error()))
		os.Exit(1)
	}
}

func usage() {
	title := t.Gradient("✧ SERAPH", t.Halo, 0, true)
	fmt.Printf(`%s  %s

Usage:
  seraph [flags]                 open the board (TUI)
  seraph run [flags] "<task>"    run one worker headless and stream it
  seraph ls                      list sessions
  seraph rm <id>                 remove a session and its worktree (branch kept)
  seraph doctor                  check that agents are installed
  seraph logo                    print the emblem

Flags (seraph and seraph run):
  -a, -agent string   claude | cursor (default "claude", env SERAPH_AGENT)
  -m, -model string   model passed to the agent CLI
  -yolo               let agents run any command without asking
  -no-worktree        work directly in the current directory (run only)
  -no-splash          skip the intro (TUI only)

State lives in %s (override with SERAPH_HOME).
`, title, t.FG(t.Dim).Render("v"+version), store.Home())
}

type common struct {
	agent, model                  string
	yolo, noWorktree, noSplash    bool
}

func parseFlags(name string, args []string) (*common, []string, error) {
	c := &common{}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = usage
	def := os.Getenv("SERAPH_AGENT")
	if def == "" {
		def = "claude"
	}
	fs.StringVar(&c.agent, "agent", def, "")
	fs.StringVar(&c.agent, "a", def, "")
	fs.StringVar(&c.model, "model", "", "")
	fs.StringVar(&c.model, "m", "", "")
	fs.BoolVar(&c.yolo, "yolo", false, "")
	fs.BoolVar(&c.noWorktree, "no-worktree", false, "")
	fs.BoolVar(&c.noSplash, "no-splash", false, "")
	if err := fs.Parse(args); err != nil {
		return nil, nil, err
	}
	if _, err := agent.Get(c.agent); err != nil {
		return nil, nil, err
	}
	return c, fs.Args(), nil
}

func (c *common) approval() agent.Approval {
	if c.yolo {
		return agent.ApprovalYolo
	}
	return agent.ApprovalSafe
}

func run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "run":
			return cmdRun(args[1:])
		case "ls", "list":
			return cmdList()
		case "rm", "remove":
			return cmdRemove(args[1:])
		case "doctor":
			return cmdDoctor()
		case "logo":
			fmt.Println(logo.Render(64, 32))
			return nil
		case "version", "--version", "-v":
			fmt.Println("seraph", version)
			return nil
		case "help", "--help", "-h":
			usage()
			return nil
		}
	}
	c, rest, err := parseFlags("seraph", args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("unknown command %q (see seraph help)", rest[0])
	}
	return cmdTUI(c)
}

func openManager() (*orch.Manager, error) {
	st, err := store.Open()
	if err != nil {
		return nil, err
	}
	return orch.New(st)
}

func cwdRepo() (dir, repo string, err error) {
	dir, err = os.Getwd()
	if err != nil {
		return "", "", err
	}
	repo, _ = workspace.RepoRoot(dir)
	return dir, repo, nil
}

func cmdTUI(c *common) error {
	mgr, err := openManager()
	if err != nil {
		return err
	}
	dir, repo, err := cwdRepo()
	if err != nil {
		return err
	}
	err = tui.Run(mgr, tui.Options{
		Repo: repo, Dir: dir, Agent: c.agent, Model: c.model,
		Approval: c.approval(), SkipSplash: c.noSplash,
	})
	running := 0
	for _, s := range mgr.Sessions() {
		if mgr.Running(s.ID) {
			running++
		}
	}
	if running > 0 {
		fmt.Println(t.FG(t.Dim).Render(fmt.Sprintf("stopping %d worker(s)…", running)))
	}
	mgr.StopAll()
	return err
}

func cmdRun(args []string) error {
	c, rest, err := parseFlags("run", args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	task := strings.TrimSpace(strings.Join(rest, " "))
	if task == "" {
		return errors.New(`usage: seraph run [-a claude|cursor] "<task>"`)
	}
	mgr, err := openManager()
	if err != nil {
		return err
	}
	dir, repo, err := cwdRepo()
	if err != nil {
		return err
	}
	if c.noWorktree {
		repo = ""
	}
	id, err := mgr.Spawn(orch.SpawnOpts{
		Task: task, Agent: c.agent, Model: c.model, Approval: c.approval(), Repo: repo, Dir: dir,
	})
	if err != nil {
		return err
	}
	s, _ := mgr.Get(id)
	fmt.Println(t.Gradient("✧ SERAPH", t.Halo, 0, true) + t.FG(t.Dim).Render(" summoned ") + t.FG(t.Ivory).Render(id) +
		t.FG(t.Dim).Render(" · "+c.agent+" · "+orDefault(s.Branch, "shared dir")))
	if s.Worktree {
		fmt.Println(t.FG(t.Faint).Render("  " + s.Dir))
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println(t.FG(t.Dim).Render("\nstopping…"))
		mgr.Stop(id)
	}()

	printed := 0
	for {
		evs, _ := mgr.Log(id)
		for ; printed < len(evs); printed++ {
			printEvent(evs[printed], c.agent)
		}
		if !mgr.Running(id) {
			evs, _ = mgr.Log(id)
			for ; printed < len(evs); printed++ {
				printEvent(evs[printed], c.agent)
			}
			break
		}
		time.Sleep(120 * time.Millisecond)
	}
	mgr.Wait()
	s, _ = mgr.Get(id)
	fmt.Println()
	switch s.Status {
	case store.StatusDone:
		msg := "✦ ascended"
		if s.Cost > 0 {
			msg += fmt.Sprintf(" · $%.4f", s.Cost)
		}
		fmt.Println(t.FG(t.Gold).Render(msg))
		if s.Worktree {
			fmt.Println(t.FG(t.Dim).Render("  " + workspace.Summary(s.Dir, s.Base) + " · branch " + s.Branch))
		}
		return nil
	case store.StatusStopped:
		return errors.New("stopped")
	default:
		return errors.New(orDefault(s.Error, string(s.Status)))
	}
}

func printEvent(ev agent.Event, agentName string) {
	switch ev.Kind {
	case agent.KindText:
		fmt.Println(t.FG(t.Gold).Bold(true).Render("✧ "+agentName) + "\n" + t.FG(t.Ivory).Render(ev.Text))
	case agent.KindToolStart:
		fmt.Println(t.FG(t.Cyan).Render("  ⟢ "+ev.Tool) + " " + t.FG(t.Dim).Render(ev.Text))
	case agent.KindToolEnd:
		if ev.IsError {
			fmt.Println(t.FG(t.Rose).Render("    ✗ " + ev.Text))
		}
	case agent.KindResult:
		if ev.IsError {
			fmt.Println(t.FG(t.Rose).Render("✗ " + ev.Text))
		}
	case agent.KindError:
		fmt.Println(t.FG(t.Rose).Render("✗ " + ev.Text))
	}
}

func cmdList() error {
	mgr, err := openManager()
	if err != nil {
		return err
	}
	list := mgr.Sessions()
	if len(list) == 0 {
		fmt.Println(t.FG(t.Dim).Render("the heavens are quiet: no sessions yet"))
		return nil
	}
	for _, s := range list {
		status := map[store.Status]string{
			store.StatusDone: t.FG(t.Gold).Render("✦ done   "), store.StatusFailed: t.FG(t.Rose).Render("✗ failed "),
			store.StatusStopped: t.FG(t.Lavender).Render("◇ stopped"), store.StatusWorking: t.FG(t.Cyan).Render("⟡ working"),
			store.StatusQueued: t.FG(t.Cyan).Render("⟡ queued "),
		}[s.Status]
		fmt.Printf("%s  %s  %-7s %s  %s\n", t.FG(t.Ivory).Render(s.ID), status, s.Agent,
			t.FG(t.Lavender).Render(orDefault(s.Branch, "-")), s.Title)
	}
	return nil
}

func cmdRemove(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: seraph rm <id> [id…]")
	}
	mgr, err := openManager()
	if err != nil {
		return err
	}
	for _, id := range args {
		s, ok := mgr.Get(id)
		if !ok {
			return fmt.Errorf("no session %s", id)
		}
		if err := mgr.Remove(id); err != nil {
			return err
		}
		msg := "released " + id
		if s.Branch != "" {
			msg += " (branch " + s.Branch + " kept)"
		}
		fmt.Println(t.FG(t.Gold).Render("✦ ") + msg)
	}
	return nil
}

func cmdDoctor() error {
	ok := true
	for _, name := range agent.Names() {
		d, _ := agent.Get(name)
		path, err := agent.Locate(d)
		if err != nil {
			ok = false
			fmt.Printf("%s %-7s %s\n", t.FG(t.Rose).Render("✗"), name, t.FG(t.Dim).Render(d.Binary()+" not found"))
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		out, _ := execOutput(ctx, path, "--version")
		cancel()
		fmt.Printf("%s %-7s %s %s\n", t.FG(t.Gold).Render("✦"), name, t.FG(t.Ivory).Render(firstLine(out)), t.FG(t.Faint).Render(path))
	}
	if _, repo, _ := cwdRepo(); repo != "" {
		fmt.Printf("%s repo    %s\n", t.FG(t.Gold).Render("✦"), repo)
	} else {
		fmt.Printf("%s repo    %s\n", t.FG(t.Lavender).Render("◇"), t.FG(t.Dim).Render("not in a git repo: workers will share the directory"))
	}
	if !ok {
		return errors.New("some agents are missing")
	}
	return nil
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
