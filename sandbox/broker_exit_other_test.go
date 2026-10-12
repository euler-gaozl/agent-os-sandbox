//go:build !linux

package sandbox

import "os"

// The Docker broker death scenario is a Linux acceptance test.
func brokerExitedBySIGKILL(*os.ProcessState) bool { return false }
