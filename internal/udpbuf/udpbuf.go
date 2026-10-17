// Package udpbuf checks what a UDP socket is granted, since quic-go and
// wireguard-go both ask for 7 MiB buffers, and logs one line of tnl's own when
// the grant is smaller.
package udpbuf

import (
	"log/slog"
	"net"
	"os"
	"syscall"
)

// Wanted is the value install.sh writes for net.core.rmem_max and wmem_max.
const Wanted = 7500000

// need is what quic-go and wireguard-go request (7 MiB); a socket that ends up
// with less had its request truncated.
const need = 7 << 20

const guide = "https://github.com/hyprsh/tnl/blob/main/docs/guide/install.md#udp-buffers"

// Granted opens a UDP socket, asks for 7 MiB receive and send buffers the way
// quic-go does (plain first, then the forced variant where the platform has
// one, which a process with CAP_NET_ADMIN is allowed) and returns the sizes it
// reads back. It measures the socket, not /proc/sys/net/core, because a
// container on a bridge network cannot read the host's limits. Tests replace
// it with udpbuftest.Fake.
var Granted = func() (recv, send int, err error) {
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
	_ = set(need)
	size, err := inspect(rc, opt)
	if err == nil && size < need {
		_ = force(rc, need)
		size, err = inspect(rc, opt)
	}
	return size, err
}

func inspect(rc syscall.RawConn, opt int) (int, error) {
	var size int
	var serr error
	if err := rc.Control(func(fd uintptr) {
		size, serr = syscall.GetsockoptInt(int(fd), syscall.SOL_SOCKET, opt)
	}); err != nil {
		return 0, err
	}
	return size, serr
}

// Warn logs one warn line when a socket is granted less than the sockets ask
// for. A host where the socket can't be measured logs nothing.
func Warn(lg *slog.Logger) {
	recv, send, err := Granted()
	if err != nil {
		return
	}
	if recv >= need && send >= need {
		return
	}
	lg.Warn("the UDP buffer limits are too low",
		"recv_buffer", recv, "send_buffer", send, "wanted", need,
		"fix", "set net.core.rmem_max and net.core.wmem_max to 7500000", "guide", guide)
}

// SilenceQuicGo turns off quic-go's own buffer line, which names no tnl
// setting. It must run before the first QUIC listener opens.
func SilenceQuicGo() error {
	return os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
}
