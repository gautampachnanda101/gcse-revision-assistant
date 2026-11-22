//go:build !windows

package main

import "syscall"

// execSelf replaces the current process image with the upgraded binary.
var execSelf = syscall.Exec
