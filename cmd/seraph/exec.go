package main

import (
	"context"
	"os/exec"
)

func execOutput(ctx context.Context, bin string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	return string(out), err
}
