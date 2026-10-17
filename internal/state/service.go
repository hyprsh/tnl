package state

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Service is a parsed Service URL.
type Service struct {
	Canonical string // the stored form
	HostPort  string // what the Outpost dials
	TLS       bool   // https://: the Edge speaks TLS to the Service
	Host      string // the URL's host, which a TLS certificate is verified against
}

// Parsed is the Route's Service URL, parsed. Validate has already accepted it,
// so it cannot fail on a Route from the state.
func (r Route) Parsed() Service {
	svc, _ := ParseService(r.Service)
	return svc
}

// HostPort is the host:port the Outpost dials for the Route.
func (r Route) HostPort() string { return r.Parsed().HostPort }

// ParseService checks a Service URL: http:// or https://, a host and an optional
// port (default 80 or 443), with no path, query, fragment or user.
func ParseService(raw string) (Service, error) {
	raw = strings.TrimSpace(raw)
	u, perr := url.Parse(raw)
	if perr != nil || u.Scheme == "" {
		return Service{}, fmt.Errorf("the Service URL %q must look like http://host:port", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Service{}, fmt.Errorf("the Service URL %q must use http:// or https://", raw)
	}
	if u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(raw[len(u.Scheme+"://"):], "?#") {
		return Service{}, fmt.Errorf("the Service URL %q must not have a path, query or fragment: a Route sends a whole Hostname to one Service", raw)
	}
	if u.User != nil {
		return Service{}, fmt.Errorf("the Service URL %q must not have a user", raw)
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return Service{}, fmt.Errorf("the Service URL %q has no host", raw)
	}
	port := 80
	if u.Scheme == "https" {
		port = 443
	}
	canonical := u.Scheme + "://"
	if strings.Contains(host, ":") {
		canonical += "[" + host + "]"
	} else {
		canonical += host
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return Service{}, fmt.Errorf("the Service URL %q has a bad port: use 1 to 65535", raw)
		}
		port = n
		canonical += ":" + p
	}
	return Service{
		Canonical: canonical, HostPort: net.JoinHostPort(host, strconv.Itoa(port)),
		TLS: u.Scheme == "https", Host: host,
	}, nil
}

// Options are the Route's options as name=value words, in a fixed order, with
// the unset ones left out. A header word is header=Name: value, so it may hold
// spaces.
func (r Route) Options() []string {
	var out []string
	if r.HostHeader != "" {
		out = append(out, "host-header="+r.HostHeader)
	}
	if r.InsecureSkipVerify {
		out = append(out, "insecure-skip-verify=true")
	}
	if r.NoHSTS {
		out = append(out, "no-hsts=true")
	}
	for _, h := range r.Headers {
		out = append(out, "header="+h)
	}
	return out
}

// CheckHostHeader refuses a --host-header value that could not be a Host header.
func CheckHostHeader(v string) error {
	for _, c := range v {
		if c <= ' ' || c == 0x7f {
			return fmt.Errorf("the host header %q must not hold spaces or control characters", v)
		}
	}
	return nil
}
