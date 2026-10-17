package state

import (
	"fmt"
	"net/textproto"
	"slices"
	"sort"
	"strings"
)

// refusedHeaders are the names a Route's --header may not set, in canonical
// form: the ones tnl manages, the hop-by-hop ones and the ones that describe
// the body.
var refusedHeaders = map[string]string{
	"Strict-Transport-Security": "use --no-hsts to turn HSTS off",
	"Via":                       "tnl manages it",
	"Server":                    "tnl manages it",
	"Content-Length":            "it describes the body",
	"Content-Type":              "it describes the body",
	"Transfer-Encoding":         "it describes the body",
	"Connection":                "it is hop-by-hop",
	"Keep-Alive":                "it is hop-by-hop",
	"Proxy-Authenticate":        "it is hop-by-hop",
	"Proxy-Authorization":       "it is hop-by-hop",
	"Proxy-Connection":          "it is hop-by-hop",
	"Te":                        "it is hop-by-hop",
	"Trailer":                   "it is hop-by-hop",
	"Upgrade":                   "it is hop-by-hop",
}

// SplitHeader splits "Name: value" at the first colon, trimming the space
// around the value. It checks nothing else.
func SplitHeader(h string) (name, value string, ok bool) {
	name, value, ok = strings.Cut(h, ":")
	return name, strings.Trim(value, " \t"), ok
}

func validName(n string) bool {
	if n == "" {
		return false
	}
	for i := 0; i < len(n); i++ {
		c := n[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0) {
			return false
		}
	}
	return true
}

// parseHeader checks one "Name: value" and returns it with its name in
// canonical form.
func parseHeader(h string) (name, value string, err error) {
	name, value, ok := SplitHeader(h)
	if !ok {
		return "", "", fmt.Errorf("the header %q must be Name: value", h)
	}
	if strings.HasPrefix(name, ":") {
		return "", "", fmt.Errorf("the header %q is a pseudo-header, which a Route may not set", h)
	}
	if !validName(name) {
		return "", "", fmt.Errorf("the header name %q is not a valid HTTP header name", name)
	}
	name = textproto.CanonicalMIMEHeaderKey(name)
	if why, bad := refusedHeaders[name]; bad {
		return "", "", fmt.Errorf("a Route may not set the header %s: %s", name, why)
	}
	if value == "" {
		return "", "", fmt.Errorf("the header %s has no value", name)
	}
	for i := 0; i < len(value); i++ {
		if c := value[i]; (c < ' ' && c != '\t') || c >= 0x7f {
			return "", "", fmt.Errorf("the value of the header %s must be printable ASCII", name)
		}
	}
	return name, value, nil
}

// CanonHeaders checks a Route's extra response headers, each "Name: value", and
// returns them in the canonical form: names in canonical case, one ": " after
// the name, no space around the value, sorted by name. A name twice is refused.
func CanonHeaders(in []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, h := range in {
		name, value, err := parseHeader(h)
		if err != nil {
			return nil, err
		}
		if seen[name] {
			return nil, fmt.Errorf("the header %s is given twice", name)
		}
		seen[name] = true
		out = append(out, name+": "+value)
	}
	sort.Strings(out)
	return out, nil
}

// CheckHeaders refuses headers that are not valid or not in canonical form
// (CanonHeaders).
func CheckHeaders(in []string) error {
	canon, err := CanonHeaders(in)
	if err != nil {
		return err
	}
	if !slices.Equal(canon, in) {
		return fmt.Errorf("the headers %q are not in canonical form (%q)", in, canon)
	}
	return nil
}

// sameRoute reports whether two Routes are equal.
func sameRoute(a, b Route) bool {
	return a.Hostname == b.Hostname && a.Outpost == b.Outpost && a.Service == b.Service &&
		a.InsecureSkipVerify == b.InsecureSkipVerify && a.HostHeader == b.HostHeader &&
		a.NoHSTS == b.NoHSTS && slices.Equal(a.Headers, b.Headers)
}
