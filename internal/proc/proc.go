// Package proc answers whether a process is still running.
package proc

import (
	"errors"
	"syscall"
)

// Alive reports whether a process with this pid exists. A process owned by
// another user still counts as alive.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
