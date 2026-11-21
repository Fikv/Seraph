//go:build windows

package agent

import (
	"os/exec"
	"time"
)

func configureProcess(cmd *exec.Cmd) {
	cmd.WaitDelay = 5 * time.Second
}
