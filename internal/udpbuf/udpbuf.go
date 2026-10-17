// Package udpbuf checks the host's UDP buffer limits, which quic-go and
// wireguard-go both ask to raise to 7 MiB, and logs one line of tnl's own when
// they are too low.
package udpbuf

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// Wanted is the value install.sh writes for net.core.rmem_max and wmem_max.
const Wanted = 7500000

// need is what quic-go and wireguard-go request (7 MiB); a limit below it
// truncates the request.
const need = 7 << 20

const guide = "https://github.com/hyprsh/tnl/blob/main/docs/guide/install.md#udp-buffers"

// Read returns a net.core sysctl ("rmem_max", "wmem_max"). Tests replace it.
var Read = func(name string) (int, error) {
	b, err := os.ReadFile("/proc/sys/net/core/" + name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

// Warn logs one warn line when either limit is below what the sockets ask for.
// A host whose limits can't be read (not Linux) logs nothing.
func Warn(lg *slog.Logger) {
	r, err := Read("rmem_max")
	if err != nil {
		return
	}
	w, err := Read("wmem_max")
	if err != nil {
		return
	}
	if r >= need && w >= need {
		return
	}
	lg.Warn("the UDP buffer limits are too low, so packets drop under load and HTTP/3 slows down: set net.core.rmem_max and net.core.wmem_max to "+strconv.Itoa(Wanted)+" (see "+guide+")",
		"rmem_max", r, "wmem_max", w, "wanted", Wanted)
}

// SilenceQuicGo turns off quic-go's own buffer line, which names no tnl
// setting. It must run before the first QUIC listener opens.
func SilenceQuicGo() {
	os.Setenv("QUIC_GO_DISABLE_RECEIVE_BUFFER_WARNING", "true")
}
