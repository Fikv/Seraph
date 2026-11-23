package main

import (
	"errors"
	"os"

	"seraph/internal/store"
	"seraph/internal/team"
)

func cmdTeam(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: seraph team <session-id>   (MCP server started by orchestrators and their subagents)")
	}
	st, err := store.Open()
	if err != nil {
		return err
	}
	defer st.Close()
	return team.Server{Store: st, SessionID: args[0]}.Serve(os.Stdin, os.Stdout)
}
