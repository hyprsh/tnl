package state

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ParseService checks a Service URL and returns it in its stored form, and the
// host:port the Outpost dials. Slice 1 takes http:// only: a host and an
// optional port (default 80), with no path, query, fragment or user.
func ParseService(raw string) (canonical, hostPort string, err error) {
	raw = strings.TrimSpace(raw)
	u, perr := url.Parse(raw)
	if perr != nil || u.Scheme == "" {
		return "", "", fmt.Errorf("the Service URL %q must look like http://host:port", raw)
	}
	if u.Scheme != "http" {
		return "", "", fmt.Errorf("the Service URL %q must use http:// (https:// Services come in a later release)", raw)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw[len("http://"):], "?#") {
		return "", "", fmt.Errorf("the Service URL %q must not have a path, query or fragment: a Route sends a whole Hostname to one Service", raw)
	}
	if u.User != nil {
		return "", "", fmt.Errorf("the Service URL %q must not have a user", raw)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", "", fmt.Errorf("the Service URL %q has no host", raw)
	}
	port := 80
	canonical = "http://"
	if strings.Contains(host, ":") {
		canonical += "[" + host + "]"
	} else {
		canonical += host
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", "", fmt.Errorf("the Service URL %q has a bad port: use 1 to 65535", raw)
		}
		port = n
		canonical += ":" + p
	}
	return canonical, net.JoinHostPort(host, strconv.Itoa(port)), nil
}
