//go:build unix

package state

import "syscall"

func setUmask(m int) int { return syscall.Umask(m) }
