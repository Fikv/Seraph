package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"seraph/internal/agent"
	"seraph/internal/store"
	t "seraph/internal/theme"
	"seraph/internal/workspace"
)

const projectUsage = `usage:
  seraph project list
  seraph project add <name> [-repo path]... [-notes dir] [-desc text] [-agent claude|cursor] [-parallel n]
  seraph project use <name>|-        (- = all projects)
  seraph project rm <name>`

const todoUsage = `usage:
  seraph todo list [-p project]
  seraph todo add [-p project] [-body text] [-prio 0-3] [-queue] "<title>"`

func cmdProject(args []string) error {
	if len(args) == 0 {
		return errors.New(projectUsage)
	}
	st, err := store.Open()
	if err != nil {
		return err
	}
	defer st.Close()
	switch args[0] {
	case "list", "ls":
		list, err := st.Projects()
		if err != nil {
			return err
		}
		cur := st.Setting(store.CurrentProjectSetting)
		if len(list) == 0 {
			fmt.Println(t.FG(t.Dim).Render("no projects yet: seraph project add <name> -repo . -notes <obsidian folder>"))
		}
		for _, p := range list {
			mark := " "
			if p.ID == cur {
				mark = t.FG(t.Gold).Render("›")
			}
			fmt.Printf("%s %s %s\n", mark, t.FG(t.Lavender).Bold(true).Render("◈ "+p.Name), t.FG(t.Faint).Render(p.ID+" · "+firstNonEmptyStr(p.DefaultAgent, "default agent")+fmt.Sprintf(" · max %d", p.MaxParallel)))
			for _, r := range p.Repos {
				fmt.Println(t.FG(t.Dim).Render("    repo   " + r))
			}
			if p.NotesDir != "" {
				fmt.Println(t.FG(t.Dim).Render("    notes  " + p.NotesDir))
			}
		}
		return nil
	case "add":
		if len(args) < 2 {
			return errors.New(projectUsage)
		}
		fs := flag.NewFlagSet("project add", flag.ContinueOnError)
		var repos multiFlag
		fs.Var(&repos, "repo", "")
		notes := fs.String("notes", "", "")
		desc := fs.String("desc", "", "")
		ag := fs.String("agent", "", "")
		parallel := fs.Int("parallel", 2, "")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		p := store.Project{ID: store.NewID(), Name: args[1], Description: *desc, DefaultAgent: *ag, MaxParallel: *parallel}
		if *ag != "" {
			if _, err := agent.Get(*ag); err != nil {
				return err
			}
		}
		for _, r := range repos {
			abs, _ := filepath.Abs(r)
			root, err := workspace.RepoRoot(abs)
			if err != nil {
				return fmt.Errorf("%s is not a git repository", r)
			}
			p.Repos = append(p.Repos, root)
		}
		if *notes != "" {
			abs, _ := filepath.Abs(*notes)
			if st, err := os.Stat(abs); err != nil || !st.IsDir() {
				return fmt.Errorf("notes folder %s does not exist", abs)
			}
			p.NotesDir = abs
		}
		if err := st.SaveProject(p); err != nil {
			return err
		}
		if err := st.SetSetting(store.CurrentProjectSetting, p.ID); err != nil {
			return err
		}
		fmt.Println(t.FG(t.Gold).Render("✦ ") + "project " + p.Name + " created and selected")
		return nil
	case "use":
		if len(args) < 2 {
			return errors.New(projectUsage)
		}
		if args[1] == "-" {
			return st.SetSetting(store.CurrentProjectSetting, "")
		}
		p, ok, err := st.Project(args[1])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no project %q", args[1])
		}
		fmt.Println(t.FG(t.Gold).Render("✦ ") + "using project " + p.Name)
		return st.SetSetting(store.CurrentProjectSetting, p.ID)
	case "rm", "remove":
		if len(args) < 2 {
			return errors.New(projectUsage)
		}
		p, ok, err := st.Project(args[1])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("no project %q", args[1])
		}
		if err := st.DeleteProject(p.ID); err != nil {
			return err
		}
		fmt.Println(t.FG(t.Gold).Render("✦ ") + "deleted project " + p.Name + " (sessions and branches stay)")
		return nil
	}
	return errors.New(projectUsage)
}

func resolveProject(st *store.Store, name string) (store.Project, error) {
	if name == "" {
		name = st.Setting(store.CurrentProjectSetting)
	}
	if name == "" {
		return store.Project{}, errors.New("no project selected: use -p <name> or `seraph project use <name>`")
	}
	p, ok, err := st.Project(name)
	if err != nil {
		return store.Project{}, err
	}
	if !ok {
		return store.Project{}, fmt.Errorf("no project %q", name)
	}
	return p, nil
}

func cmdTodo(args []string) error {
	if len(args) == 0 {
		return errors.New(todoUsage)
	}
	st, err := store.Open()
	if err != nil {
		return err
	}
	defer st.Close()
	fs := flag.NewFlagSet("todo", flag.ContinueOnError)
	proj := fs.String("p", "", "")
	body := fs.String("body", "", "")
	prio := fs.Int("prio", 1, "")
	queue := fs.Bool("queue", false, "")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	p, err := resolveProject(st, *proj)
	if err != nil {
		return err
	}
	switch args[0] {
	case "list", "ls":
		todos, err := st.Todos(p.ID)
		if err != nil {
			return err
		}
		fmt.Println(t.FG(t.Lavender).Bold(true).Render("◈ " + p.Name))
		for _, status := range store.TodoStatuses {
			for _, td := range todos {
				if td.Status != status {
					continue
				}
				extra := ""
				if td.SessionID != "" {
					extra = " · worker " + td.SessionID
				}
				fmt.Printf("  %-7s %s %s\n", t.FG(t.Dim).Render(string(td.Status)), td.Title, t.FG(t.Faint).Render(td.ID+strings.Repeat("▲", td.Priority)+extra))
			}
		}
		return nil
	case "add":
		title := strings.TrimSpace(strings.Join(fs.Args(), " "))
		if title == "" {
			return errors.New(todoUsage)
		}
		td := store.Todo{ID: store.NewID(), ProjectID: p.ID, Title: title, Body: *body, Priority: min(max(*prio, 0), 3), Status: store.TodoOpen}
		if *queue {
			td.Status = store.TodoQueued
		}
		if err := st.SaveTodo(td); err != nil {
			return err
		}
		fmt.Println(t.FG(t.Gold).Render("✦ ") + "added " + td.ID + " to " + p.Name + " (" + string(td.Status) + ")")
		return nil
	}
	return errors.New(todoUsage)
}

func firstNonEmptyStr(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
