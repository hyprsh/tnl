//go:build !linux

package udpbuf

import "syscall"

// Only Linux has a forced variant.
func forceRecv(syscall.RawConn, int) error { return nil }
func forceSend(syscall.RawConn, int) error { return nil }
