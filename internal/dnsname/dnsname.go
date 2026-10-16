// Package dnsname holds the one definition of a DNS name that the Edge's API,
// the state checks and the router share. It imports nothing outside the
// standard library, so the Outpost-only build can use it too.
package dnsname

import (
	"errors"
	"net"
	"strings"
)

// Normalize makes a name comparable: no surrounding space, lower case, no
// trailing dot.
func Normalize(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}

// HostOnly normalises the host of a "host" or "host:port" string, such as a
// request's Host header. IPv6 brackets are removed.
func HostOnly(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return Normalize(host)
	}
	return Normalize(strings.Trim(hostport, "[]"))
}

// CheckLabel accepts one lower-case DNS label: 1 to 63 letters, digits and '-',
// not starting or ending with '-'. The error is a reason, phrased to follow the
// name it is about.
func CheckLabel(label string) error {
	switch {
	case label == "" || len(label) > 63:
		return errors.New("each label must be 1 to 63 characters")
	case label[0] == '-' || label[len(label)-1] == '-':
		return errors.New("a label must not start or end with '-'")
	}
	for _, c := range label {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return errors.New("only lower-case letters, digits and '-' are allowed in a label")
		}
	}
	return nil
}

// CheckName accepts a normalised name of dot-separated labels, at most 253
// characters long.
func CheckName(name string) error {
	if len(name) > 253 {
		return errors.New("longer than 253 characters")
	}
	for _, label := range strings.Split(name, ".") {
		if err := CheckLabel(label); err != nil {
			return err
		}
	}
	return nil
}

// LongestZone returns the longest of the zones that contains host: the zone
// whose name host is, or ends with after a dot. A partial label does not count,
// so "evilexample.com" is not in "example.com". Names are normalised.
// Validation, the router and the certificate manager all ask this one question.
func LongestZone(host string, zones []string) (string, bool) {
	best, found := "", false
	for _, z := range zones {
		if (host == z || strings.HasSuffix(host, "."+z)) && (!found || len(z) > len(best)) {
			best, found = z, true
		}
	}
	return best, found
}
