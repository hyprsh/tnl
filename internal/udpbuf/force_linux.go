package udpbuf

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// The forced variants go beyond net.core.{r,w}mem_max with CAP_NET_ADMIN.
func forceRecv(rc syscall.RawConn, n int) error { return setsock(rc, unix.SO_RCVBUFFORCE, n) }
func forceSend(rc syscall.RawConn, n int) error { return setsock(rc, unix.SO_SNDBUFFORCE, n) }

func setsock(rc syscall.RawConn, opt, n int) error {
	var serr error
	if err := rc.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, opt, n)
	}); err != nil {
		return err
	}
	return serr
}
