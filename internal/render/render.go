// Package render prints the replies of tnl's `ls` and `status` commands, as
// tables and text or as the JSON that is the stable surface. It imports only
// api, so the Outpost build links it and the harness can print what a real
// Edge answered.
//
// A table is made from a reply alone: it never reads a clock, because the
// reply carries its durations in seconds. The one exception is OutpostStatus,
// whose "last answer" is a time, so it is given the time to count from.
// Tables may change in any release; the JSON may only grow (docs/spec/v1.md,
// Status).
package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/hyprsh/tnl/internal/api"
)

// Dash is an empty cell: a value that is not known.
const Dash = "–"

// JSON writes v as `--json` prints it: indented, with a final newline.
func JSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// RawJSON writes a reply as it came, indented, with a final newline.
func RawJSON(w io.Writer, raw []byte) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return err
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}

// Span is a length of time in its largest whole unit: "3d", "5h", "42m", "4s".
func Span(seconds int64) string {
	switch {
	case seconds >= 86400:
		return fmt.Sprintf("%dd", seconds/86400)
	case seconds >= 3600:
		return fmt.Sprintf("%dh", seconds/3600)
	case seconds >= 60:
		return fmt.Sprintf("%dm", seconds/60)
	}
	return fmt.Sprintf("%ds", max(seconds, 0))
}

// OneLine cuts an error to its first line, and to a length a table can hold.
func OneLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	s = strings.TrimSpace(s)
	const most = 100
	if len(s) > most {
		s = s[:most-3] + "..."
	}
	return s
}

// Count is "1 Route" or "3 Routes": n and the noun in its number.
func Count(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func orDash(s string) string {
	if s == "" {
		return Dash
	}
	return s
}

func table(w io.Writer, header []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, row := range rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	return tw.Flush()
}

// DeclaredMark is added to the name of an Outpost or Zone that the Edge's
// settings declare.
const DeclaredMark = " (declared)"

// Outposts prints `outpost ls`.
func Outposts(w io.Writer, l api.OutpostList) error {
	var rows [][]string
	for _, o := range l.Outposts {
		name := o.Name
		if o.Declared {
			name += DeclaredMark
		}
		seen := Dash
		switch {
		case o.State == api.StateNotJoined && o.TokenExpiresInSeconds != nil && *o.TokenExpiresInSeconds > 0:
			seen = "token expires in " + Span(*o.TokenExpiresInSeconds)
		case o.State == api.StateNotJoined:
			seen = "expired"
		case o.LastSeenAgoSeconds != nil:
			seen = Span(*o.LastSeenAgoSeconds) + " ago"
		}
		routes := fmt.Sprint(o.Routes)
		if o.DeclaredLines > 0 {
			routes = fmt.Sprintf("%d declared", o.DeclaredLines)
		}
		rows = append(rows, []string{name, string(o.State), seen, orDash(o.Endpoint), orDash(o.Version), o.TunnelAddress, routes})
	}
	return table(w, []string{"NAME", "STATE", "LAST SEEN", "ENDPOINT", "VERSION", "TUNNEL ADDRESS", "ROUTES"}, rows)
}

// Routes prints `route ls`. Fields the reply has that this build does not know
// go in a last column as name=value.
func Routes(w io.Writer, l api.RouteList) error {
	header := []string{"HOSTNAME", "OUTPOST", "SERVICE", "OPTIONS", "STATUS"}
	extra := false
	var rows [][]string
	for _, r := range l.Routes {
		row := []string{r.Hostname, r.Outpost, r.Service, orDash(optionWords(r.Options)), orDash(string(r.Status))}
		if len(r.Extra) > 0 {
			extra = true
		}
		rows = append(rows, append(row, unknownFields(r.Extra)))
	}
	if extra {
		header = append(header, "MORE")
	} else {
		for i := range rows {
			rows[i] = rows[i][:len(header)]
		}
	}
	return table(w, header, rows)
}

// optionWords joins the options with spaces. A header word has spaces in it,
// so its value is quoted: header="Name: value".
func optionWords(opts []string) string {
	words := make([]string, len(opts))
	for i, o := range opts {
		words[i] = o
		if name, v, ok := strings.Cut(o, "="); ok && strings.ContainsAny(v, " \t\"") {
			words[i] = name + "=" + strconv.Quote(v)
		}
	}
	return strings.Join(words, " ")
}

func unknownFields(extra map[string]json.RawMessage) string {
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	words := make([]string, 0, len(keys))
	for _, k := range keys {
		var s string
		v := string(extra[k])
		if json.Unmarshal(extra[k], &s) == nil {
			v = s
		}
		words = append(words, k+"="+v)
	}
	return strings.Join(words, " ")
}

// Zones prints `zone ls`.
func Zones(w io.Writer, l api.ZoneList) error {
	var rows [][]string
	for _, z := range l.Zones {
		name := z.Name
		if z.Declared {
			name += DeclaredMark
		}
		rows = append(rows, []string{name, z.Mode, orDash(z.Provider), ZoneCertificate(z)})
	}
	return table(w, []string{"ZONE", "MODE", "PROVIDER", "CERTIFICATE"}, rows)
}

// ZoneCertificate is the CERTIFICATE cell: the wildcard's own state, or a
// summary of the Hostnames' certificates.
func ZoneCertificate(z api.ZoneRow) string {
	if c := z.Certificate; c != nil {
		return certificate(*c)
	}
	failing := 0
	for _, c := range z.Certificates {
		if c.Failing() {
			failing++
		}
	}
	s := Count(len(z.Certificates), "certificate", "certificates")
	if failing > 0 {
		s += fmt.Sprintf(", %d failing", failing)
	}
	return s
}

func certificate(c api.CertInfo) string {
	switch c.State {
	case api.CertValid:
		if c.Expires != nil {
			return "valid until " + c.Expires.UTC().Format("2006-01-02")
		}
		return "valid"
	case api.CertRenewalFailing:
		s := "renewal failing"
		if c.FailingForSeconds != nil {
			s += " for " + Span(*c.FailingForSeconds)
		}
		if c.Error != "" {
			s += ": " + OneLine(c.Error)
		}
		return s
	case api.CertFailed:
		if c.Error != "" {
			return "failed: " + OneLine(c.Error)
		}
		return "failed"
	}
	return string(c.State) // obtaining, expired
}
