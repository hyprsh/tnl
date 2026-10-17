package api

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/hyprsh/tnl/internal/logx"
)

// PathLogs streams the process's own recent log lines from its in-memory ring.
// GET, local only, and answered by the process the socket belongs to: an
// Outpost's relay answers it itself and never forwards it. Query: n (the last
// n lines), since (a Go duration), level (error, warn, info or debug), follow
// (1 to keep the reply open) and format (json to force JSON lines). The reply
// is text/plain, one line per log line, flushed as they come.
const PathLogs = "/v1/logs"

// LogsQuery is what `tnl logs` asks for.
type LogsQuery struct {
	Follow bool
	N      int           // the last N lines; 0 for all held
	Since  time.Duration // only lines newer than this; 0 for no limit
	Level  string        // only lines at this level or above; "" for all
	JSON   bool          // JSON lines whatever the process logs
}

// Values is the query string of q.
func (q LogsQuery) Values() url.Values {
	v := url.Values{}
	if q.Follow {
		v.Set("follow", "1")
	}
	if q.N > 0 {
		v.Set("n", strconv.Itoa(q.N))
	}
	if q.Since > 0 {
		v.Set("since", q.Since.String())
	}
	if q.Level != "" {
		v.Set("level", q.Level)
	}
	if q.JSON {
		v.Set("format", "json")
	}
	return v
}

// ParseLogsQuery reads the query of a GET PathLogs; the error is a 400.
func ParseLogsQuery(v url.Values) (LogsQuery, error) {
	var q LogsQuery
	q.Follow = v.Get("follow") == "1"
	q.JSON = v.Get("format") == "json"
	if s := v.Get("n"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return q, Errorf(http.StatusBadRequest, "n %q: want a number of lines", s)
		}
		q.N = n
	}
	if s := v.Get("since"); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 {
			return q, Errorf(http.StatusBadRequest, "since %q: want a duration such as 10m", s)
		}
		q.Since = d
	}
	if s := v.Get("level"); s != "" {
		if _, ok := ParseLogLevel(s); !ok {
			return q, Errorf(http.StatusBadRequest, "level %q: want error, warn, info or debug", s)
		}
		q.Level = s
	}
	return q, nil
}

// ParseLogLevel reads error, warn, info or debug.
func ParseLogLevel(s string) (slog.Level, bool) {
	switch s {
	case "error":
		return slog.LevelError, true
	case "warn":
		return slog.LevelWarn, true
	case "info":
		return slog.LevelInfo, true
	case "debug":
		return slog.LevelDebug, true
	}
	return 0, false
}

// ServeLogs answers a GET PathLogs from ring. The caller has checked the
// caller's right. It returns an error, to be sent as a refusal, only before
// anything is written. The caller sets the Tnl-Version header.
func ServeLogs(w http.ResponseWriter, r *http.Request, ring *logx.Ring) error {
	q, err := ParseLogsQuery(r.URL.Query())
	if err != nil {
		return err
	}
	if ring == nil {
		return Errorf(http.StatusServiceUnavailable, "this process keeps no log ring")
	}
	min := slog.Level(-1 << 30)
	if q.Level != "" {
		min, _ = ParseLogLevel(q.Level)
	}
	var cutoff time.Time
	if q.Since > 0 {
		cutoff = time.Now().Add(-q.Since)
	}
	json := q.JSON || ring.JSON()
	keep := func(e logx.Entry) bool { return e.Level >= min && !e.Time.Before(cutoff) }

	var snap []logx.Entry
	var live <-chan logx.Entry
	if q.Follow {
		var stop func()
		snap, live, stop = ring.Follow()
		defer stop()
	} else {
		snap = ring.Snapshot()
	}
	var held []logx.Entry
	for _, e := range snap {
		if keep(e) {
			held = append(held, e)
		}
	}
	if q.N > 0 && len(held) > q.N {
		held = held[len(held)-q.N:]
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	flush := func() {
		if fl != nil {
			fl.Flush()
		}
	}
	for _, e := range held {
		w.Write([]byte(e.Line(json) + "\n"))
	}
	flush()
	if live == nil {
		return nil
	}
	for {
		select {
		case <-r.Context().Done():
			return nil
		case e := <-live:
			if keep(e) {
				if _, err := w.Write([]byte(e.Line(json) + "\n")); err != nil {
					return nil
				}
				flush()
			}
		}
	}
}
