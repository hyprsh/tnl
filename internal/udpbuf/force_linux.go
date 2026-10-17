package udpbuf

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// The forced variants go beyond net.core.{r,w}mem_max with CAP_NET_ADMIN.
func forceRecv(rc syscall.RawConn, n int) error { return setsock(rc, unix.SO_RCVBUFFORCE, n) }
func forceSend(rc syscall.RawConn, n int) error { return setsock(rc, unix.SO_SNDBUFFORCE, n) }

func setsock(rc syscall.RawConn, opt, n int) error {
	return control(rc, func(fd int) error { return unix.SetsockoptInt(fd, unix.SOL_SOCKET, opt, n) })
}

// granted halves what getsockopt reads back: Linux reports twice the size it
// granted, to count its bookkeeping overhead.
func granted(readBack int) int { return readBack / 2 }
