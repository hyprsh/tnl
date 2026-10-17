package harness

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/edge/liveness"
)

// testSilentAfter shrinks the 20 s of Outpost silence. A live Outpost pings
// every 100 ms here, so it is never silent that long.
const testSilentAfter = 1500 * time.Millisecond

// silentService serves what the silence bound must not cut:
//
//	/upload       reads one byte, signals started, reads the rest and answers
//	/upload-late  reads the whole body, then answers after a pause
//	/upload-stall reads one byte, stops reading for a pause, then reads the rest
//	/download     writes a large body
//	/sse          an event stream
type silentService struct {
	*Service
	pause   time.Duration
	started chan struct{}
}

const bigBody = 48 << 20

func newSilentService(t *testing.T, pause time.Duration) *silentService {
	s := &silentService{pause: pause, started: make(chan struct{}, 8)}
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		first := make([]byte, 1)
		io.ReadFull(r.Body, first)
		s.started <- struct{}{}
		n, _ := io.Copy(io.Discard, r.Body)
		io.WriteString(w, "got "+strconv.Itoa(int(n)+1))
	})
	mux.HandleFunc("/upload-late", func(w http.ResponseWriter, r *http.Request) {
		n, _ := io.Copy(io.Discard, r.Body)
		time.Sleep(s.pause)
		io.WriteString(w, "got "+strconv.Itoa(int(n)))
	})
	mux.HandleFunc("/upload-stall", func(w http.ResponseWriter, r *http.Request) {
		first := make([]byte, 1)
		io.ReadFull(r.Body, first)
		time.Sleep(s.pause)
		n, _ := io.Copy(io.Discard, r.Body)
		io.WriteString(w, "got "+strconv.Itoa(int(n)+1))
	})
	mux.HandleFunc("/download", func(w http.ResponseWriter, r *http.Request) {
		chunk := bytes.Repeat([]byte("x"), 64<<10)
		for sent := 0; sent < bigBody; sent += len(chunk) {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	})
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		rc := http.NewResponseController(w)
		for {
			if _, err := io.WriteString(w, "data: x\n\n"); err != nil || rc.Flush() != nil {
				return
			}
			select {
			case <-time.After(20 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	s.Service = NewHTTPService(t, mux)
	return s
}

func silentEdge(t *testing.T, svc *silentService) (*RealEdge, *JoinedOutpost, *lockedBuf) {
	t.Helper()
	return silentEdgeWith(t, svc, EdgeOptions{SilentAfter: testSilentAfter})
}

// silentEdgeWith is silentEdge with the options the test needs, over the
// Pebble, the log and the level it always sets.
func silentEdgeWith(t *testing.T, svc *silentService, opts EdgeOptions) (*RealEdge, *JoinedOutpost, *lockedBuf) {
	t.Helper()
	logs := new(lockedBuf)
	opts.Pebble, opts.LogOut, opts.LogLevel = StartPebble(t, PebbleOptions{}), logs, slog.LevelInfo
	re := StartEdgeWith(t, opts)
	re.AddZone(t, "test")
	o := re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	get(t, re.Caddy, H1, "app.test", "/")
	return re, o, logs
}

// zeros is an upload body of n bytes that is read as fast as the transport
// takes it.
func zeros(n int) io.Reader {
	return io.LimitReader(zeroReader{}, int64(n))
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

// When an Outpost's packets stop during a Visitor's upload, the Edge closes the
// Outpost's Tunnel connections after the silence bound, and the Outpost is not
// Offline yet (here the default 60 s).
func TestAnUploadEndsWithinTheSilenceBoundWhenTheOutpostGoesQuiet(t *testing.T) {
	svc := newSilentService(t, 0)
	re, o, logs := silentEdge(t, svc)
	client := re.Caddy.Client(t, H1)

	pr, pw := io.Pipe()
	req, _ := http.NewRequestWithContext(ctx(t), "POST", "https://app.test/upload", pr)
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := client.Do(req)
		if err != nil {
			done <- result{err: err}
			return
		}
		resp.Body.Close()
		done <- result{code: resp.StatusCode}
	}()
	pw.Write([]byte("hello "))
	<-svc.started

	cutLink(t, o, re)
	cut := time.Now()
	// Keep the upload moving, so only the Edge's close can end it.
	go func() {
		for {
			if _, err := pw.Write(make([]byte, 16<<10)); err != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	select {
	case r := <-done:
		if d := time.Since(cut); d > testSilentAfter+4*time.Second {
			t.Fatalf("the upload ended %v after the cut, want about %v", d, testSilentAfter)
		}
		if r.err == nil && r.code != http.StatusBadGateway {
			t.Fatalf("the upload ended with status %d, want the Edge's 502", r.code)
		}
	case <-time.After(testSilentAfter + 6*time.Second):
		t.Fatal("the upload did not end")
	}
	pw.Close()
	if re.Liveness.State("nas") == liveness.Offline {
		t.Fatal("the Outpost went Offline first: the test shows nothing about the silence bound")
	}
	waitFor(t, "a request line saying connection lost", 3*time.Second, func() bool {
		return strings.Contains(logs.String(), "reason=\"connection lost\"")
	})
}

// A stream already answering when the Outpost goes quiet is closed too, and its
// request line says why, apart from the Offline close.
func TestAStreamCutBySilenceIsLoggedAsOutpostSilent(t *testing.T) {
	svc := newSilentService(t, 0)
	re, o, logs := silentEdge(t, svc)
	next := openStream(t, re, "app.test")
	if !next() {
		t.Fatal("the stream did not start")
	}
	cutLink(t, o, re)
	cut := time.Now()
	for next() {
	}
	if d := time.Since(cut); d > testSilentAfter+4*time.Second {
		t.Fatalf("the stream ended %v after the cut", d)
	}
	if re.Liveness.State("nas") == liveness.Offline {
		t.Fatal("the Outpost went Offline first")
	}
	waitFor(t, "the request line", 3*time.Second, func() bool {
		return strings.Contains(logs.String(), "reason=\"outpost silent\"")
	})
}

// A Service that reads the whole body and answers later than the bound is not
// cut: its Outpost keeps pinging.
func TestASlowServiceThatAnswersAfterTheBoundIsNotCut(t *testing.T) {
	svc := newSilentService(t, 3*testSilentAfter)
	re, _, _ := silentEdge(t, svc)
	req, _ := http.NewRequestWithContext(ctx(t), "POST", "https://app.test/upload-late", strings.NewReader("hello"))
	resp, err := re.Caddy.Client(t, H1).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(b) != "got 5" {
		t.Fatalf("got %d %q", resp.StatusCode, b)
	}
}

// A Service that stops reading an upload for longer than the bound leaves a
// zero window that the Outpost's stack keeps acknowledging; the upload is not
// cut and finishes.
func TestAServiceThatStopsReadingAnUploadLongerThanTheBoundIsNotCut(t *testing.T) {
	svc := newSilentService(t, 3*testSilentAfter)
	re, _, _ := silentEdge(t, svc)
	req, _ := http.NewRequestWithContext(ctx(t), "POST", "https://app.test/upload-stall", zeros(bigBody))
	req.ContentLength = bigBody
	start := time.Now()
	resp, err := re.Caddy.Client(t, H1).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(b) != "got "+strconv.Itoa(bigBody) {
		t.Fatalf("got %d %q", resp.StatusCode, b)
	}
	if time.Since(start) < svc.pause {
		t.Fatalf("the upload finished in %v: the Service's pause did not stall it, so the test shows nothing", time.Since(start))
	}
}

// A Visitor that stops reading a download for longer than the bound is not cut.
func TestAVisitorThatStopsReadingADownloadLongerThanTheBoundIsNotCut(t *testing.T) {
	svc := newSilentService(t, 0)
	re, _, _ := silentEdge(t, svc)
	req, _ := http.NewRequestWithContext(ctx(t), "GET", "https://app.test/download", nil)
	resp, err := re.Caddy.Client(t, H1).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	first := make([]byte, 1)
	if _, err := io.ReadFull(resp.Body, first); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * testSilentAfter)
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil || n+1 != bigBody {
		t.Fatalf("read %d of %d bytes after the pause: %v", n+1, bigBody, err)
	}
}
