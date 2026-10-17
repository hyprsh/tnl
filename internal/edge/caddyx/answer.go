package caddyx

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/hyprsh/tnl/internal/dnsname"
	"github.com/hyprsh/tnl/internal/wire"
)

// eventStreamRetry is the reconnect delay, in milliseconds, the Edge gives an
// event-stream request it would otherwise answer with a 502 or 503 (ADR 0013).
const eventStreamRetry = 5000

// eventStreamReply is the Edge's reply to such a request: 200 with a retry:
// field, then the stream ends. It is built here and nowhere else, so every
// header the Edge puts on its own replies goes on it.
func eventStreamReply(req *http.Request) *http.Response {
	body := "retry: " + strconv.Itoa(eventStreamRetry) + "\n\n"
	h := http.Header{}
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        h,
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

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
func (rt *Runtime) answer(w http.ResponseWriter, req *http.Request, id string, status int, reason string, eventStream bool) {
	host := dnsname.HostOnly(req.Host)
	noteAnswer(req, status, reason, eventStream)
	writeErrorPage(w, errorPage{Status: status, Host: host, ID: id, Accept: req.Header.Get("Accept"), Time: time.Now()})
}

// fail is the Transport's answer for a Service or Outpost it can't reach: the
// page as a synthetic response, or for an event stream a 200 with a retry: field
// (ADR 0013). The log keeps the real status either way.
func (rt *Runtime) fail(req *http.Request, id string, status int, reason string) (*http.Response, error) {
	if isEventStream(req) {
		noteAnswer(req, status, reason, true)
		return eventStreamReply(req), nil
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
