//go:build unix

package kubectl

import "syscall"

// processAlive reports whether a process with pid still exists: signal 0
// checks for it without sending anything.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
