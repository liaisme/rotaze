//go:build windows

package cli

import "syscall"

// Windows has no setsid; the equivalent detach is a new process group plus
// DETACHED_PROCESS so the child does not hold the parent's console.
// CREATE_NEW_PROCESS_GROUP = 0x00000200, DETACHED_PROCESS = 0x00000008.
const detachedProcess = 0x00000008

func detachedSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: 0x00000200 | detachedProcess,
	}
}
