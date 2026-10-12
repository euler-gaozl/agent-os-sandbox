package main

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureChild(cmd *exec.Cmd) error {
	if os.Geteuid() != 0 {
		return errors.New("trusted guard requires a different UID from the workload")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534, Groups: []uint32{65534}}, Setpgid: true}
	return nil
}
