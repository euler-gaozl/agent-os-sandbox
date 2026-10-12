// sandbox-guard is the trusted container entry process, never a host executor.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"
)

func main() { os.Exit(run(os.Args[1:])) }
func run(args []string) int {
	if len(args) != 1 || len(args[0]) > 256<<10 {
		fmt.Fprintln(os.Stderr, "one bounded script is required")
		return 125
	}
	cmd := exec.Command("/usr/local/bin/node", "--eval", args[0])
	if e := configureChild(cmd); e != nil {
		fmt.Fprintln(os.Stderr, "sandbox guard identity unavailable")
		return 125
	}
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/tmp", "TMPDIR=/tmp", "NODE_OPTIONS=--max-old-space-size=64"}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if e := cmd.Start(); e != nil {
		fmt.Fprintln(os.Stderr, "sandbox workload could not start")
		return 125
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case e := <-done:
		if e == nil {
			return 0
		}
		if exit, ok := e.(*exec.ExitError); ok && exit.ExitCode() >= 0 {
			return exit.ExitCode()
		}
		return 125
	case <-timer.C:
		// Exit the main process. Docker init then exits the PID namespace and
		// the kernel destroys all descendants, including stopped grandchildren.
		// No CAP_KILL or process signal from an equal-UID watchdog is needed.
		fmt.Fprintln(os.Stderr, "sandbox hard deadline reached")
		return 124
	}
}
