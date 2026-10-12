package sandbox

import (
	"os"
	"syscall"
)

func brokerExitedBySIGKILL(state *os.ProcessState) bool {
	if state == nil {
		return false
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGKILL
}
