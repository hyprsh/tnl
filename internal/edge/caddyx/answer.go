package caddyx

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/hyprsh/tnl/internal/dnsname"
	"github.com/hyprsh/tnl/internal/wire"
)

// errResetStream is what the Transport returns for an event-stream request it
// would otherwise answer with a 502 or 503; the Router turns it into a reset.
var errResetStream = errors.New("tnl: event-stream request reset instead of answered")

func isEventStream(req *http.Request) bool {
	return strings.Contains(strings.ToLower(strings.Join(req.Header.Values("Accept"), ",")), "text/event-stream")
}

func (rt *Runtime) logger() *slog.Logger {
	if rt.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return rt.Log
}

// answer writes the error page and notes why, for the request line the Router
// writes when the request ends.
func (rt *Runtime) answer(w http.ResponseWriter, req *http.Request, id string, status int, reason string, reset bool) {
	host := dnsname.HostOnly(req.Host)
	noteAnswer(req, status, reason, reset)
	writeErrorPage(w, errorPage{Status: status, Host: host, ID: id, Accept: req.Header.Get("Accept"), Time: time.Now()})
}

// fail is the Transport's answer for a Service or Outpost it can't reach: the
// page as a synthetic response, or errResetStream for an event stream.
func (rt *Runtime) fail(req *http.Request, id string, status int, reason string) (*http.Response, error) {
	if isEventStream(req) {
		noteAnswer(req, status, reason, true)
		return nil, errResetStream
	}
	rec := httptest.NewRecorder()
	rt.answer(rec, req, id, status, reason, false)
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

// replyReason is the log's reason for a reply that isn't ok.
func replyReason(re *ReplyError) string {
	if re.Reply.Status == wire.StatusDialError {
		return re.Reply.Reason.String()
	}
	return "outpost incompatible"
}
