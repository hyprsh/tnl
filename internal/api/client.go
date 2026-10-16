package api

import (
	"bytes"
	"cmp"
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
	"github.com/hyprsh/tnl/internal/version"
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

// link is how a Client reaches its peer: over tnl.sock, or through the Tunnel.
// A Client picks one, here, and nothing else asks which.
type link interface {
	// address is where the peer is, for messages.
	address() string
	dial(ctx context.Context) (net.Conn, error)
	// dialError says what a failed dial or request means to the operator.
	dialError(err error) error
	// mustMatchVersion is whether the peer must be exactly this client's
	// version: the local process must; an Edge through the Tunnel may be any
	// version it accepts.
	mustMatchVersion() bool
}

func (c *Client) link() link {
	if c.Dial != nil {
		return tunnelLink{via: c.Dial}
	}
	return socketLink{path: filepath.Join(c.StateDir, SocketName)}
}

// tunnelLink is the Edge's API at 100.64.0.1:TunnelPort inside the Tunnel.
type tunnelLink struct {
	via func(ctx context.Context, network, address string) (net.Conn, error)
}

func (l tunnelLink) address() string {
	return net.JoinHostPort(addr.Edge.String(), strconv.Itoa(TunnelPort))
}

func (l tunnelLink) dial(ctx context.Context) (net.Conn, error) {
	return l.via(ctx, "tcp", l.address())
}

func (l tunnelLink) mustMatchVersion() bool { return false }

func (l tunnelLink) dialError(err error) error {
	return fmt.Errorf("can't reach the Edge's API at %s inside the Tunnel: %w", l.address(), err)
}

// socketLink is a tnl.sock in a state directory.
type socketLink struct{ path string }

func (l socketLink) address() string { return l.path }

func (l socketLink) dial(ctx context.Context) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", l.path)
}

func (l socketLink) mustMatchVersion() bool { return true }

func (l socketLink) dialError(err error) error {
	switch {
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return fmt.Errorf("can't open %s: permission denied; run with sudo", l.path)
	case errors.Is(err, syscall.ENOENT), errors.Is(err, syscall.ECONNREFUSED):
		return fmt.Errorf("no Edge is running: nothing answers on %s (start it with `tnl edge`, or pass --state-dir or set TNL_STATE_DIR to the state directory of the one to talk to)", l.path)
	}
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Op == "dial" {
		return fmt.Errorf("no Edge is running: can't connect to %s: %v", l.path, oe.Err)
	}
	return err
}

func (c *Client) http() *http.Client {
	l := c.link()
	return &http.Client{Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext:       func(ctx context.Context, _, _ string) (net.Conn, error) { return l.dial(ctx) },
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
	raw, err := c.roundTrip(ctx, method, path, req)
	if err != nil {
		return out, err
	}
	if err := strictDecoder(raw).Decode(&out); err != nil {
		return out, c.notUnderstood(err)
	}
	return out, nil
}

// Fetch is Call for a reply the client prints as it came: a GET of an `ls` or
// status reply. It reads the reply leniently, since JSON only grows within a
// major and an older client must still show what it knows of a newer peer's
// reply, and returns the bytes too, for `--json`.
func Fetch[Resp any](ctx context.Context, c *Client, path string) (Resp, []byte, error) {
	var out Resp
	raw, err := c.roundTrip(ctx, http.MethodGet, path, nil)
	if err != nil {
		return out, nil, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, nil, c.notUnderstood(err)
	}
	return out, raw, nil
}

func (c *Client) notUnderstood(err error) error {
	return fmt.Errorf("the running process (%s) sent a reply this tnl doesn't understand: %w", c.Version, err)
}

// roundTrip sends one request and returns the body of a successful response.
func (c *Client) roundTrip(ctx context.Context, method, path string, req any) ([]byte, error) {
	var out []byte
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
		return out, c.link().dialError(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return out, err
	}
	if resp.Header.Get(VersionHeader) == "" {
		return out, fmt.Errorf("%s answered without a %s header; it is not a tnl", c.link().address(), VersionHeader)
	}
	if resp.StatusCode >= 400 {
		var eb errorBody
		msg := strings.TrimSpace(string(raw))
		if dec := strictDecoder(raw); dec.Decode(&eb) == nil && eb.Error != "" {
			msg = eb.Error
		}
		ae := &Error{Status: resp.StatusCode, Message: msg, UnknownField: resp.Header.Get(UnknownFieldHeader)}
		if note, ok := FieldAdded[ae.UnknownField]; ok {
			ae.Message = version.UnknownField(resp.Header.Get(VersionHeader), cmp.Or(note.Name, ae.UnknownField), note.Since)
		}
		return out, ae
	}
	return raw, nil
}

// Info asks the process at the other end of the link, the socket or the Tunnel, for its role, version and build, and
// refuses unless its version is exactly this client's, where the link requires
// that. Over the Tunnel it only asks.
func (c *Client) Info(ctx context.Context) (Info, error) {
	info, err := Call[Info](ctx, c, http.MethodGet, PathInfo, nil)
	if err != nil {
		return info, err
	}
	if c.link().mustMatchVersion() && info.Version != c.Version {
		return info, versionMismatch(c.Version, info)
	}
	return info, nil
}

func (c *Client) checkLocal(ctx context.Context) error {
	if !c.link().mustMatchVersion() {
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
