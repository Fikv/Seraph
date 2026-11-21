package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"seraph/internal/mcp"
	t "seraph/internal/theme"
)

const mcpUsage = `usage:
  seraph mcp list
  seraph mcp search [query]
  seraph mcp add <registry-name> [-name n] [-option i] [-e KEY=VALUE]... [-no-claude] [-no-cursor]
  seraph mcp test <name>
  seraph mcp rm <name>`

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func cmdMCP(args []string) error {
	if len(args) == 0 {
		return errors.New(mcpUsage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cfg, err := mcp.Load()
	if err != nil {
		return err
	}
	switch args[0] {
	case "list", "ls":
		return mcpList(cfg)
	case "search":
		return mcpSearch(ctx, strings.Join(args[1:], " "))
	case "add", "install":
		return mcpAdd(ctx, cfg, args[1:])
	case "test", "check":
		if len(args) < 2 {
			return errors.New(mcpUsage)
		}
		s, ok := cfg.Get(args[1])
		if !ok {
			return fmt.Errorf("no server %q", args[1])
		}
		mcp.Check(ctx, s)
		if err := cfg.Save(); err != nil {
			return err
		}
		printServer(*s)
		return nil
	case "rm", "remove":
		if len(args) < 2 {
			return errors.New(mcpUsage)
		}
		if _, ok := cfg.Get(args[1]); !ok {
			return fmt.Errorf("no server %q", args[1])
		}
		if err := mcp.Remove(ctx, args[1]); err != nil {
			return err
		}
		cfg.Delete(args[1])
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Println(t.FG(t.Gold).Render("✦ ") + "removed " + args[1])
		return nil
	}
	return errors.New(mcpUsage)
}

func mcpList(cfg *mcp.Config) error {
	if len(cfg.Servers) == 0 {
		fmt.Println(t.FG(t.Dim).Render("no MCP servers yet: seraph mcp search <query>"))
	}
	for _, s := range cfg.Servers {
		printServer(s)
	}
	for _, e := range mcp.Externals(cfg) {
		fmt.Printf("%s %-24s %s\n", t.FG(t.Faint).Render("◇"), e.Name, t.FG(t.Dim).Render("external · "+e.Target))
	}
	return nil
}

func printServer(s mcp.Server) {
	status := t.FG(t.Dim).Render("· untested")
	switch s.Status {
	case mcp.StatusOK:
		status = t.FG(t.Mint).Render(fmt.Sprintf("✓ %d tools", len(s.Tools)))
	case mcp.StatusAuth:
		status = t.FG(t.Lavender).Render("◇ needs login")
	case mcp.StatusError:
		status = t.FG(t.Rose).Render("✗ error")
	}
	var targets []string
	if s.Claude {
		targets = append(targets, t.FG(t.Gold).Render("✻ claude"))
	}
	if s.Cursor {
		targets = append(targets, t.FG(t.Cyan).Render("◆ cursor"))
	}
	fmt.Printf("%s %-24s %-6s %-12s %s\n", t.FG(t.Gold).Render("✦"), s.Name, s.Transport, status, strings.Join(targets, " "))
	fmt.Println(t.FG(t.Faint).Render("    " + s.CommandLine()))
	if s.Error != "" {
		fmt.Println(t.FG(t.Rose).Render("    " + s.Error))
	}
	if len(s.Tools) > 0 {
		fmt.Println(t.FG(t.Dim).Render("    tools: " + strings.Join(s.Tools, ", ")))
	}
}

func mcpSearch(ctx context.Context, q string) error {
	entries, _, err := mcp.Search(ctx, q, "", 30)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println(t.FG(t.Dim).Render("nothing found"))
	}
	for _, e := range entries {
		var kinds []string
		for _, o := range mcp.Options(e) {
			kinds = append(kinds, o.Label)
		}
		fmt.Printf("%s %s %s\n", t.FG(t.Gold).Render("✦"), t.FG(t.Ivory).Bold(true).Render(e.Name), t.FG(t.Dim).Render(e.Version+" · "+strings.Join(kinds, ", ")))
		if e.Description != "" {
			fmt.Println(t.FG(t.Dim).Render("    " + e.Description))
		}
	}
	return nil
}

func mcpAdd(ctx context.Context, cfg *mcp.Config, args []string) error {
	fs := flag.NewFlagSet("mcp add", flag.ContinueOnError)
	name := fs.String("name", "", "")
	option := fs.Int("option", 0, "")
	noClaude := fs.Bool("no-claude", false, "")
	noCursor := fs.Bool("no-cursor", false, "")
	var envs multiFlag
	fs.Var(&envs, "e", "")
	if len(args) == 0 {
		return errors.New(mcpUsage)
	}
	reg := args[0]
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	entry, err := mcp.Lookup(ctx, reg)
	if err != nil {
		return err
	}
	opts := mcp.Options(entry)
	if len(opts) == 0 {
		return fmt.Errorf("%s has no installable package or remote", reg)
	}
	if *option < 0 || *option >= len(opts) {
		return fmt.Errorf("option must be 0..%d", len(opts)-1)
	}
	values := map[string]string{}
	for _, kv := range envs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("-e expects KEY=VALUE, got %q", kv)
		}
		values[k] = v
	}
	n := *name
	if n == "" {
		n = cfg.UniqueName(mcp.ShortName(entry.Name))
	}
	s, err := opts[*option].Build(n, values)
	if err != nil {
		var missing []string
		for _, f := range opts[*option].Fields {
			if f.Required && values[f.Name] == "" && f.Default == "" {
				missing = append(missing, f.Name)
			}
		}
		return fmt.Errorf("%w (set with -e: %s)", err, strings.Join(missing, ", "))
	}
	s.Claude, s.Cursor = !*noClaude, !*noCursor
	fmt.Println(t.FG(t.Dim).Render("downloading and testing " + s.CommandLine() + " …"))
	mcp.Check(ctx, &s)
	if err := mcp.Apply(ctx, s); err != nil {
		return err
	}
	cfg.Put(s)
	if err := cfg.Save(); err != nil {
		return err
	}
	printServer(s)
	return nil
}
