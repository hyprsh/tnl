// Package udpbuf checks what a UDP socket is granted, since quic-go and
// wireguard-go both ask for 7 MiB buffers, and logs one line of tnl's own when
// the grant is smaller.
package udpbuf

import (
	"log/slog"
	"net"
	"os"
	"strconv"
	"syscall"
)

// SysctlValue is the value install.sh writes for net.core.rmem_max and
// wmem_max, a little over Request so the kernel grants all of it.
const SysctlValue = 7500000

// Request is what quic-go and wireguard-go ask each socket for (7 MiB); a
// socket granted less had its request truncated.
const Request = 7 << 20

const guide = "https://github.com/hyprsh/tnl/blob/main/docs/guide/install.md#udp-buffers"

// Measure reports the sizes a socket was granted. Configs carry one that is
// nil in production, and tests set it to a fixed answer.
type Measure func() (recv, send int, err error)

// Granted opens a UDP socket, asks for 7 MiB receive and send buffers the way
// quic-go does (plain first, then the forced variant where the platform has
// one, which a process with CAP_NET_ADMIN is allowed) and returns the sizes the
// kernel granted (Linux reads back twice the grant, and that is halved). It
// measures the socket, not /proc/sys/net/core, because a container on a bridge
// network cannot read the host's limits.
func Granted() (recv, send int, err error) {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return 0, 0, err
	}
	defer c.Close()
	rc, err := c.SyscallConn()
	if err != nil {
		return 0, 0, err
	}
	recv, err = grant(rc, c.SetReadBuffer, syscall.SO_RCVBUF, forceRecv)
	if err != nil {
		return 0, 0, err
	}
	send, err = grant(rc, c.SetWriteBuffer, syscall.SO_SNDBUF, forceSend)
	return recv, send, err
}

func grant(rc syscall.RawConn, set func(int) error, opt int, force func(syscall.RawConn, int) error) (int, error) {
	// Errors from the requests are ignored: the read-back says whether they worked.
	_ = set(Request)
	size, err := inspect(rc, opt)
	if err == nil && size < Request {
		_ = force(rc, Request)
		size, err = inspect(rc, opt)
	}
	return size, err
}

func inspect(rc syscall.RawConn, opt int) (int, error) {
	var size int
	err := control(rc, func(fd int) (err error) {
		size, err = syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, opt)
		return err
	})
	return granted(size), err
}

// control runs fn on the socket's descriptor and returns fn's error, or
// Control's own.
func control(rc syscall.RawConn, fn func(fd int) error) error {
	var ferr error
	if err := rc.Control(func(fd uintptr) { ferr = fn(int(fd)) }); err != nil {
		return err
	}
	return ferr
}

// Warn logs one warn line when a socket is granted less than the sockets ask
// for. A nil measure measures a real socket; a host where the socket can't be
// measured logs nothing.
func Warn(lg *slog.Logger, measure Measure) {
	if measure == nil {
		measure = Granted
	}
	recv, send, err := measure()
	if err != nil {
		return
	}
	if recv >= Request && send >= Request {
		return
	}
	lg.Warn("the UDP buffer limits are too low",
		"recv_buffer", recv, "send_buffer", send, "wanted", Request,
		"fix", "set net.core.rmem_max and net.core.wmem_max to "+strconv.Itoa(SysctlValue), "guide", guide)
}

// SilenceQuicGo turns off quic-go's own buffer line, which names no tnl
// setting. It must run before the first QUIC listener opens.
func SilenceQuicGo() error {
	return os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
}
