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
	"strings"
	"syscall"
)

// Client talks to the process whose tnl.sock is in StateDir.
type Client struct {
	StateDir string
	Version  string // this tnl's version; the running process must report exactly it
}

func (c *Client) socket() string { return filepath.Join(c.StateDir, SocketName) }

func (c *Client) http() *http.Client {
	return &http.Client{Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", c.socket())
		},
	}}
}

// Call sends one request and decodes the response into Resp. req is the JSON
// body, or nil for none. A refusal comes back as *Error. Both sides refuse
// fields they don't know, and the client refuses a process that isn't the
// same version.
func Call[Resp any](ctx context.Context, c *Client, method, path string, req any) (Resp, error) {
	var out Resp
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
	if v := resp.Header.Get(VersionHeader); v != c.Version {
		if v == "" {
			return out, fmt.Errorf("%s answered without a %s header; it is not a tnl", c.socket(), VersionHeader)
		}
		return out, fmt.Errorf("this tnl is %s but the running process is %s; restart it so both run the same version", c.Version, v)
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

func strictDecoder(raw []byte) *json.Decoder {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d
}

func (c *Client) dialError(err error) error {
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
