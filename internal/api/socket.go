package api

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// ListenSocket listens on tnl.sock in dir with mode 0600. It replaces a socket
// file nothing answers on, and refuses one that is live.
func ListenSocket(dir string) (net.Listener, error) {
	path := filepath.Join(dir, SocketName)
	if _, err := os.Lstat(path); err == nil {
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return nil, fmt.Errorf("another process is already serving %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		if len(path) > 100 {
			return nil, fmt.Errorf("%w (the socket path %s is too long for a Unix socket; use a shorter state directory)", err, path)
		}
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}
