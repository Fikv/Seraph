package main

import (
	"errors"
	"os"

	"seraph/internal/permit"
	"seraph/internal/store"
)

func cmdPermit(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: seraph permit <session-id>   (MCP server started by agents in confirm mode)")
	}
	st, err := store.Open()
	if err != nil {
		return err
	}
	defer st.Close()
	return permit.Server{Store: st, SessionID: args[0]}.Serve(os.Stdin, os.Stdout)
}
