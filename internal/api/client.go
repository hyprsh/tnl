package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/hyprsh/tnl/internal/addr"
)

// Client talks to the process whose tnl.sock is in StateDir, or, when Dial is
// set, to the Edge's API at 100.64.0.1:TunnelPort inside the Tunnel.
type Client struct {
	StateDir string
	Version  string // this tnl's version; the running process must report exactly it

	// Dial opens a connection through the Tunnel (an Outpost passes its
	// netstack's dialer). Set, StateDir is unused.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)

	mu       sync.Mutex
	verified bool
}

func (c *Client) socket() string {
	if c.Dial != nil {
		return net.JoinHostPort(addr.Edge.String(), strconv.Itoa(TunnelPort))
	}
	return filepath.Join(c.StateDir, SocketName)
}

func (c *Client) http() *http.Client {
	return &http.Client{Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			if c.Dial != nil {
				return c.Dial(ctx, "tcp", c.socket())
			}
			return (&net.Dialer{}).DialContext(ctx, "unix", c.socket())
		},
	}}
}

// Call sends one request and decodes the response into Resp. req is the JSON
// body, or nil for none. A refusal comes back as *Error. Both sides refuse
// fields they don't know, and the client refuses a process that isn't the
// same version: Call asks a local process for its info before the first
// request and refuses unless the versions match exactly. (An Edge reached
// through the Tunnel may be any version the Edge accepts.)
func Call[Resp any](ctx context.Context, c *Client, method, path string, req any) (Resp, error) {
	var out Resp
	if path != PathInfo {
		if err := c.checkLocal(ctx); err != nil {
			return out, err
		}
	}
	var body io.Reader
	if req != nil {
		b, err := json.Marshal(req)
		if err != nil {
			return out, err
		}
		body = bytes.NewReader(b)
	}
	hreq, err := http.NewRequestWithContext(ctx, method, "http://tnl"+path, body)
	if err != nil {
		return out, err
	}
	hreq.Header.Set(VersionHeader, c.Version)
	if req != nil {
		hreq.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http().Do(hreq)
	if err != nil {
		return out, c.dialError(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return out, err
	}
	if resp.Header.Get(VersionHeader) == "" {
		return out, fmt.Errorf("%s answered without a %s header; it is not a tnl", c.socket(), VersionHeader)
	}
	if resp.StatusCode >= 400 {
		var eb errorBody
		msg := strings.TrimSpace(string(raw))
		if dec := strictDecoder(raw); dec.Decode(&eb) == nil && eb.Error != "" {
			msg = eb.Error
		}
		return out, &Error{Status: resp.StatusCode, Message: msg}
	}
	if err := strictDecoder(raw).Decode(&out); err != nil {
		return out, fmt.Errorf("the running process (%s) sent a reply this tnl doesn't understand: %w", c.Version, err)
	}
	return out, nil
}

// Info asks the process on the socket for its role, version and build, and
// refuses unless its version is exactly this client's. Over the Tunnel
// (Dial set) it only asks.
func (c *Client) Info(ctx context.Context) (Info, error) {
	info, err := Call[Info](ctx, c, http.MethodGet, PathInfo, nil)
	if err != nil {
		return info, err
	}
	if c.Dial == nil && info.Version != c.Version {
		return info, versionMismatch(c.Version, info)
	}
	return info, nil
}

func (c *Client) checkLocal(ctx context.Context) error {
	if c.Dial != nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.verified {
		return nil
	}
	if _, err := c.Info(ctx); err != nil {
		return err
	}
	c.verified = true
	return nil
}

func versionMismatch(mine string, running Info) error {
	unit := "tnl-edge"
	if running.Role == RoleOutpost {
		unit = "tnl-outpost"
	}
	what := "Edge"
	if running.Role == RoleOutpost {
		what = "Outpost"
	}
	return fmt.Errorf("this `tnl` is %s but the running %s is %s; restart it (`sudo systemctl restart %s`)",
		mine, what, running.Version, unit)
}

func strictDecoder(raw []byte) *json.Decoder {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d
}

func (c *Client) dialError(err error) error {
	if c.Dial != nil {
		return fmt.Errorf("can't reach the Edge's API at %s inside the Tunnel: %w", c.socket(), err)
	}
	switch {
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return fmt.Errorf("can't open %s: permission denied; run with sudo", c.socket())
	case errors.Is(err, syscall.ENOENT), errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("no Edge is running: nothing answers on %s (start it with `tnl edge`, or pass --state-dir or set TNL_STATE_DIR to the state directory of the one to talk to)", c.socket())
	}
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Op == "dial" {
		return fmt.Errorf("no Edge is running: can't connect to %s: %v", c.socket(), oe.Err)
	}
	return err
}
