package caddyx

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/caddyserver/caddy/v2/modules/caddyhttp"

	"github.com/hyprsh/tnl/internal/dnsname"
	"github.com/hyprsh/tnl/internal/edge/liveness"
)

const (
	varActive = "tnl.active"
	varReason = "tnl.reason"
	varStatus = "tnl.status"
	varReset  = "tnl.reset"
)

// active is one request in flight, as the Router sees it. The drain reaches a
// request through it: cancelling its context and closing the response body
// ends the proxying, whatever protocol the Visitor speaks.
type active struct {
	cancel context.CancelFunc

	mu     sync.Mutex
	closer io.Closer
	stream bool
	cut    bool
	rt     *Runtime
}

// attach is called by the Transport with the Service's response. An event
// stream or an upgrade (WebSocket) is a stream, and a stream that starts while
// the Edge is draining is cut at once.
func (a *active) attach(resp *http.Response) {
	stream := resp.StatusCode == http.StatusSwitchingProtocols ||
		strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")
	a.mu.Lock()
	a.closer = resp.Body
	a.stream = a.stream || stream
	cutNow := a.stream && a.rt.draining()
	a.mu.Unlock()
	if cutNow {
		a.doCut()
	}
}

func (a *active) isStream() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stream
}

func (a *active) wasCut() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cut
}

func (a *active) doCut() {
	a.mu.Lock()
	a.cut = true
	c := a.closer
	a.mu.Unlock()
	a.cancel()
	if c != nil {
		c.Close()
	}
}

// requests is the set of requests in flight and whether the Edge is draining.
// It lives in the Runtime, outside every module, so reloads don't lose it.
type requests struct {
	mu   sync.Mutex
	m    map[*active]struct{}
	drng bool
}

func (rt *Runtime) draining() bool {
	rt.reqs.mu.Lock()
	defer rt.reqs.mu.Unlock()
	return rt.reqs.drng
}

func (rt *Runtime) begin(cancel context.CancelFunc, stream bool) *active {
	a := &active{cancel: cancel, stream: stream, rt: rt}
	rt.reqs.mu.Lock()
	if rt.reqs.m == nil {
		rt.reqs.m = map[*active]struct{}{}
	}
	rt.reqs.m[a] = struct{}{}
	drng := rt.reqs.drng
	rt.reqs.mu.Unlock()
	if drng && stream {
		a.doCut()
	}
	return a
}

func (rt *Runtime) end(a *active) {
	rt.reqs.mu.Lock()
	delete(rt.reqs.m, a)
	rt.reqs.mu.Unlock()
}

// DrainResult is what a drain did.
type DrainResult struct {
	// Drained is how many requests were in flight and finished on their own.
	Drained int
	// StreamsCut is how many event streams and WebSockets were ended at once.
	StreamsCut int
	// Closed is how many requests were still running after the timeout and were
	// closed.
	Closed int
}

// Drain is steps 2 to 4 of stopping (spec section 6). The caller has stopped
// accepting connections. Drain ends every event stream and WebSocket at once,
// waits up to timeout for the other requests to finish, moving on as soon as
// they are done, and then closes whatever remains. It returns once the
// handlers have ended or a short grace after the forced close has passed.
func (rt *Runtime) Drain(timeout time.Duration) DrainResult {
	var res DrainResult
	rt.reqs.mu.Lock()
	rt.reqs.drng = true
	var streams, others []*active
	for a := range rt.reqs.m {
		if a.isStream() {
			streams = append(streams, a)
		} else {
			others = append(others, a)
		}
	}
	rt.reqs.mu.Unlock()

	for _, a := range streams {
		a.doCut()
	}
	res.StreamsCut = len(streams)

	// Moving on as soon as the others are done.
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
wait:
	for {
		if rt.running(others, true) == 0 {
			break
		}
		select {
		case <-deadline.C:
			break wait
		case <-tick.C:
		}
	}
	// Whatever is left is closed, including connections that are only in their
	// header exchange. A request that turned into a stream meanwhile was cut.
	extra := 0
	for _, a := range others {
		if !rt.isActive(a) {
			continue
		}
		if a.isStream() {
			res.StreamsCut++
			extra++
		} else {
			res.Closed++
		}
		a.doCut()
	}
	res.Drained = len(others) - res.Closed - extra
	if res.Closed > 0 {
		rt.Conns.CloseAll()
	}
	// Let the handlers write their lines and abort their streams.
	grace := time.NewTimer(2 * time.Second)
	defer grace.Stop()
	for rt.running(append(streams, others...), false) > 0 {
		select {
		case <-grace.C:
			return res
		case <-tick.C:
		}
	}
	return res
}

// running counts the requests of as that are still in flight and could finish
// on their own: streams that were cut don't hold the drain.
func (rt *Runtime) running(as []*active, skipStreams bool) int {
	rt.reqs.mu.Lock()
	defer rt.reqs.mu.Unlock()
	n := 0
	for _, a := range as {
		if _, ok := rt.reqs.m[a]; ok {
			a.mu.Lock()
			holds := !(skipStreams && a.stream)
			a.mu.Unlock()
			if holds {
				n++
			}
		}
	}
	return n
}

func (rt *Runtime) isActive(a *active) bool {
	rt.reqs.mu.Lock()
	defer rt.reqs.mu.Unlock()
	_, ok := rt.reqs.m[a]
	return ok
}

// noteAnswer records, for the request line, that the Edge answered this request
// itself, and why.
func noteAnswer(req *http.Request, status int, reason string, reset bool) {
	ctx := req.Context()
	caddyhttp.SetVar(ctx, varReason, reason)
	caddyhttp.SetVar(ctx, varStatus, status)
	caddyhttp.SetVar(ctx, varReset, reset)
}

// recorder wraps the response writer to count the bytes and remember the
// status. It forwards everything else through Unwrap, so flushing an event
// stream and hijacking a WebSocket keep working.
type recorder struct {
	http.ResponseWriter
	status int
	bytes  atomic.Int64
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 && code >= 200 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes.Add(int64(n))
	return n, err
}

func (r *recorder) Flush() {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	http.NewResponseController(r.ResponseWriter).Flush()
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// countBody counts the bytes the Visitor sends.
type countBody struct {
	io.ReadCloser
	n atomic.Int64
}

func (c *countBody) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// requestLog is what the Router knows about one request when it ends.
type requestLog struct {
	req     *http.Request
	id      string
	outpost string
	rec     *recorder
	body    *countBody
	started time.Time
	a       *active
	failed  error // what next returned or panicked with
	inZone  bool
	vars    func(string) any
}

// write logs the request line, if this request gets one. A response the Edge
// made itself gets one at info (a 404 outside every Zone at debug); a request
// the Service answered gets one only with --access-log.
func (rt *Runtime) writeRequestLine(l requestLog) {
	reason, _ := l.vars(varReason).(string)
	reset, _ := l.vars(varReset).(bool)
	status := l.rec.status
	if st, ok := l.vars(varStatus).(int); ok && status == 0 {
		status = st
	}
	level := slog.LevelInfo
	switch {
	case reason != "":
		if reason == "no route" && !l.inZone {
			level = slog.LevelDebug
		}
	case l.failed != nil && !l.a.wasCut() && rt.outpostOffline(l.outpost):
		// The Outpost's connection dropped under a request that was running.
		reason = "outpost offline"
	case rt.AccessLog:
	default:
		return
	}
	log := rt.logger()
	if !log.Enabled(context.Background(), level) {
		return
	}
	attrs := []any{
		"id", l.id,
		"visitor", l.req.RemoteAddr,
		"host", dnsname.HostOnly(l.req.Host),
		"method", l.req.Method,
		"path", l.req.URL.Path,
		"proto", l.req.Proto,
		"status", status,
	}
	if reset {
		attrs = append(attrs, "reset", true)
	}
	bytesIn := int64(0)
	if l.body != nil {
		bytesIn = l.body.n.Load()
	}
	attrs = append(attrs, "duration", time.Since(l.started).Round(time.Millisecond), "bytes_in", bytesIn, "bytes_out", l.rec.bytes.Load())
	if l.outpost != "" {
		attrs = append(attrs, "outpost", l.outpost)
	}
	if reason != "" {
		attrs = append(attrs, "reason", reason)
	}
	log.Log(context.Background(), level, "request", attrs...)
}

func (rt *Runtime) outpostOffline(name string) bool {
	return name != "" && rt.Liveness != nil && rt.Liveness.State(name) == liveness.Offline
}
