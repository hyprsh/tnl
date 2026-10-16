package render

import (
	"fmt"
	"io"
	"time"

	"github.com/hyprsh/tnl/internal/api"
)

// OutpostStatus prints `tnl status` on an Outpost: its own side, the Edge, its
// Routes and declared lines, a blank line, and what needs attention. now is
// the time "last answer" is relative to.
func OutpostStatus(w io.Writer, s api.OutpostStatus, now time.Time) error {
	fmt.Fprintf(w, "Outpost %s, %s\n", s.Name, versionLine(s.Version, s.Build, s.Revision))
	allow := s.Allow
	if allow == "" {
		allow = "no limit"
	}
	tunnel := "up"
	if s.Edge.State == api.TunnelDown {
		tunnel = "no answer for " + Span(s.Edge.SilentSeconds)
	}
	answer := "not yet"
	if s.Edge.LastAnswer != nil {
		answer = Span(int64(now.Sub(*s.Edge.LastAnswer).Seconds())) + " ago"
	}
	edgeVersion := orDash(s.Edge.Version)
	if s.Edge.Message != "" {
		edgeVersion += ", " + s.Edge.Message
	}
	if err := fields(w, [][2]string{
		{"Tunnel address", s.TunnelAddress},
		{"State directory", s.StateDir},
		{"Allow", allow},
		{"Edge", fmt.Sprintf("%s (%s)", s.Edge.Address, orDash(s.Edge.IP))},
		{"Tunnel", tunnel},
		{"Last answer", answer},
		{"Edge version", edgeVersion},
	}); err != nil {
		return err
	}
	fmt.Fprintln(w)
	if s.Routes == nil {
		fmt.Fprintln(w, "Routes: "+s.RoutesUnavailable)
	} else if len(s.Routes) == 0 {
		fmt.Fprintln(w, "Routes: none")
	} else {
		fmt.Fprintln(w, "Routes")
		for _, r := range s.Routes {
			fmt.Fprintf(w, "  %s -> %s\n", r.Hostname, r.Service)
		}
	}
	if len(s.Declared) > 0 {
		fmt.Fprintln(w, "\nDeclared")
		for _, d := range s.Declared {
			line := fmt.Sprintf("  %s: %s -> %s", d.Variable, d.Hostname, d.Service)
			if d.Waiting {
				line += " (waiting: " + d.Reason + ")"
			}
			fmt.Fprintln(w, line)
		}
	}
	fmt.Fprintln(w)
	return Attention(w, s.Attention)
}
