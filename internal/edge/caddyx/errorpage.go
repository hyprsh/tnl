package caddyx

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"time"
)

// errorPage is one answer the Edge makes itself (spec section 6, "What Visitors
// see when the Edge answers").
type errorPage struct {
	Status int
	Host   string
	ID     string // the request ID the tnl header carries
	Accept string // the Visitor's Accept header
	Time   time.Time
}

// message is what the page says: 503 offline, 502 not answering, 404 nothing.
func (p errorPage) message() string {
	switch p.Status {
	case http.StatusServiceUnavailable:
		return p.Host + " is offline"
	case http.StatusBadGateway:
		return p.Host + " isn't answering"
	default:
		return "Nothing is served at " + p.Host
	}
}

func (p errorPage) acceptsHTML() bool {
	return strings.Contains(strings.ToLower(p.Accept), "text/html")
}

var pageTemplate = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Status}}</title>
<style>
:root{color-scheme:light dark;--fg:#1a1a1a;--bg:#fff;--muted:#777;--rule:#ccc}
@media (prefers-color-scheme: dark){:root{--fg:#e8e8e8;--bg:#121212;--muted:#8a8a8a;--rule:#444}}
html,body{height:100%;margin:0}
body{display:flex;flex-direction:column;align-items:center;justify-content:center;background:var(--bg);color:var(--fg);font:16px/1.4 system-ui,sans-serif}
.row{display:flex;align-items:center;gap:1.25rem}
.code{font-size:2rem;font-weight:600}
.rule{width:1px;align-self:stretch;background:var(--rule)}
.msg{font-size:1.1rem}
small{margin-top:1.5rem;color:var(--muted);font-size:.75rem}
</style>
</head>
<body>
<div class="row"><span class="code">{{.Status}}</span><span class="rule"></span><span class="msg">{{.Message}}</span></div>
<small>{{.When}} &middot; {{.ID}}</small>
</body>
</html>
`))

// ownHeaders are set on every reply the Edge makes itself: the page has inline
// CSS, no script and no external request, so nothing else is allowed.
var ownHeaders = [][2]string{
	{"X-Content-Type-Options", "nosniff"},
	{"X-Frame-Options", "DENY"},
	{"Referrer-Policy", "no-referrer"},
	{"Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'"},
}

// setOwnHeaders hardens a reply the Edge makes itself and removes the Server
// header Caddy sets on every response. The Edge's own replies send none.
func setOwnHeaders(h http.Header) {
	for _, kv := range ownHeaders {
		h.Set(kv[0], kv[1])
	}
	h.Del("Server")
}

// writeErrorPage answers with the built-in page, or one line of text if the
// Visitor doesn't accept HTML. Every answer is no-store.
func writeErrorPage(w http.ResponseWriter, p errorPage) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	setOwnHeaders(h)
	if !p.acceptsHTML() {
		h.Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(p.Status)
		fmt.Fprintf(w, "%d %s\n", p.Status, p.message())
		return
	}
	h.Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(p.Status)
	pageTemplate.Execute(w, struct {
		Status        int
		Message, When string
		ID            string
	}{p.Status, p.message(), p.Time.UTC().Format("2006-01-02 15:04:05 UTC"), p.ID})
}
