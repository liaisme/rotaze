//go:build !windows

package cli

import "syscall"

// detachedSysProcAttr starts the forked daemon in its own session so it is
// detached from the controlling terminal and survives the parent exiting.
func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setsid: true, // Create new session
	}
}
