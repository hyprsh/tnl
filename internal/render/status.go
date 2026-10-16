package render

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/hyprsh/tnl/internal/api"
)

// NothingToAttendTo is what `tnl status` prints when all is well.
const NothingToAttendTo = "Nothing needs attention"

// Attention prints the "Needs attention" block: one line for each problem, or
// that nothing needs attention. `tnl status` on an Outpost prints the same.
func Attention(w io.Writer, items []api.AttentionItem) error {
	if len(items) == 0 {
		_, err := fmt.Fprintln(w, NothingToAttendTo)
		return err
	}
	fmt.Fprintln(w, "Needs attention")
	for _, it := range items {
		fmt.Fprintf(w, "  - %s\n", it.Message)
	}
	return nil
}

// versionLine is "tnl 1.4.2 (release, abc123)"; the revision is left out when
// there is none.
func versionLine(version, build, revision string) string {
	if revision != "" {
		build += ", " + revision
	}
	return fmt.Sprintf("tnl %s (%s)", version, build)
}

// fields prints a block of "Name:  value" lines, the values aligned.
func fields(w io.Writer, rows [][2]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, r := range rows {
		fmt.Fprintf(tw, "%s:\t%s\n", r[0], r[1])
	}
	return tw.Flush()
}

// EdgeStatus prints `tnl status` on the Edge: the summary, a blank line, and
// what needs attention.
func EdgeStatus(w io.Writer, s api.EdgeStatus) error {
	fmt.Fprintln(w, versionLine(s.Version, s.Build, s.Revision))
	onOff := map[bool]string{true: "on", false: "off"}
	certs := fmt.Sprintf("%d", s.Certificates.Count)
	if s.Certificates.SoonestExpiry != nil {
		certs += ", the soonest expires " + s.Certificates.SoonestExpiry.UTC().Format("2006-01-02")
	}
	if err := fields(w, [][2]string{
		{"Uptime", Span(s.UptimeSeconds)},
		{"Address", fmt.Sprintf("%s, WireGuard port %d", s.Address, s.WireGuardPort)},
		{"Key", s.KeyFingerprint},
		{"State directory", s.StateDir},
		{"Log", fmt.Sprintf("level %s, access log %s", s.LogLevel, onOff[s.AccessLog])},
		{"Outposts", outpostCounts(s.Outposts)},
		{"Routes", fmt.Sprint(s.Routes)},
		{"Zones", fmt.Sprint(s.Zones)},
		{"Certificates", certs},
	}); err != nil {
		return err
	}
	fmt.Fprintln(w)
	return Attention(w, s.Attention)
}

func outpostCounts(c api.OutpostCounts) string {
	var parts []string
	for _, p := range []struct {
		n    int
		what string
	}{{c.Online, "online"}, {c.Offline, "offline"}, {c.Incompatible, "incompatible"}, {c.NotJoined, "not joined"}} {
		if p.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}
