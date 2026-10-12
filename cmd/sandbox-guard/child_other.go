//go:build !linux

package main

import (
	"errors"
	"os/exec"
)

func configureChild(*exec.Cmd) error { return errors.New("Linux container required") }
