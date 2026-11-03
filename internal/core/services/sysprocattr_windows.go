//go:build windows

package services

import "syscall"

// setDetach configures SysProcAttr to detach the process on Windows.
//
// Windows has no setsid(); CREATE_NEW_PROCESS_GROUP keeps the detached ssh
// process from receiving Ctrl+C events aimed at the parent console, and
// DETACHED_PROCESS removes it from the parent's console entirely.
func setDetach(attr *syscall.SysProcAttr) {
	const (
		createNewProcessGroup = 0x00000200
		detachedProcess       = 0x00000008
	)
	attr.CreationFlags |= createNewProcessGroup | detachedProcess
}
