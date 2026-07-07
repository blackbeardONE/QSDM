//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func launchBackground(args []string) (int, error) {
	executable, err := os.Executable()
	if err != nil {
		return 0, err
	}
	command := exec.Command(executable, args...)
	command.Env = append(os.Environ(), "QSDM_EDGE_AGENT_BACKGROUND=1")
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return 0, err
	}
	return command.Process.Pid, command.Process.Release()
}
