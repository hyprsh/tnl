package caddysuite

// The Caddy suite: the checks of research issues #10 and #12, run in process
// with Caddy's timeouts shrunk to 2 s so that pauses longer than every timeout
// fit into seconds.
//
// CADDY IS UPGRADED ONLY IN A PR OF ITS OWN. Caddy's timeout and reload
// behaviour changed twice in two weeks during research (v2.11.6 added idle
// read and write timeouts that cut streams, v2.11.7 fixed them). This suite,
// which runs on every CI run, is what tells such a change apart from the rest
// of a diff. If it fails after an upgrade, read the failing test's comment: the
// "known limits" tests fail when Caddy gets better too, and then the limit and
// the spec's section 6 should be updated.
//
// Known limits of section 6 and where they are covered:
//   - A Service that stops reading an upload for longer than read_idle_timeout
//     fails it over HTTP/2 only: TestUploadWhoseServiceStopsReadingLongerThanReadIdleFailsOnHTTP2Only.
//   - A Visitor that stalls an upload for longer than read_idle_timeout fails
//     on every protocol: TestUploadWhoseVisitorStallsLongerThanReadIdleFailsOnEveryProtocol.
//   - HTTP/2 uploads top out at about 1 MiB per round trip (Go's server
//     window; Caddy has no knob). Documented only: loopback has no round trip
//     worth measuring. TestUpload250MBReachesTheServiceIntact still pushes
//     250 MB through HTTP/2.
//   - An HTTP/3 connection dies after 30 s of silence unless the client PINGs,
//     and a 20 s heartbeat keeps it alive (quic-go's idle timeout cannot be
//     shrunk through Caddy). Documented only, since asserting it needs a 35 s
//     pause on every CI run; the research check (`quic`) did it. The heartbeat
//     test below covers the mechanism with gaps longer than the shrunk timeouts.

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/edge/caddyx"
	"github.com/hyprsh/tnl/internal/edge/routes"

	. "github.com/hyprsh/tnl/internal/harness"
)

// shrunk is the research check's setting: every Caddy timeout at 2 s.
var shrunk = caddyx.Timeouts{ReadHeader: 2 * time.Second, Idle: 2 * time.Second, ReadIdle: 2 * time.Second, WriteIdle: 2 * time.Second}

// suiteService is the Service of the suite.
//
//	/sse?gaps=3s,3s   writes an event, then one after each gap, each stamped with its write time
//	/upload?stallAfter=N&stall=4s  reads the body, optionally pausing after N bytes, and reports its size and SHA-256
func suiteService() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		var gaps []time.Duration
		for _, g := range strings.Split(r.URL.Query().Get("gaps"), ",") {
			if d, err := time.ParseDuration(g); err == nil {
				gaps = append(gaps, d)
			}
		}
		rc := http.NewResponseController(w)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		write := func(seq int) bool {
			_, err := fmt.Fprintf(w, "data: %d %d\n\n", seq, time.Now().UnixNano())
			return err == nil && rc.Flush() == nil
		}
		if !write(0) {
			return
		}
		for i, g := range gaps {
			select {
			case <-time.After(g):
			case <-r.Context().Done():
				return
			}
			if !write(i + 1) {
				return
			}
		}
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		stallAfter, _ := strconv.ParseInt(r.URL.Query().Get("stallAfter"), 10, 64)
		stall, _ := time.ParseDuration(r.URL.Query().Get("stall"))
		h := sha256.New()
		var n int64
		var err error
		if stallAfter > 0 {
			var m int64
			m, err = io.CopyN(h, r.Body, stallAfter)
			n += m
			time.Sleep(stall)
		}
		if err == nil {
			var m int64
			m, err = io.Copy(h, r.Body)
			n += m
		}
		rep := uploadReport{Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil))}
		if err != nil {
			rep.Err = err.Error()
		}
		json.NewEncoder(w).Encode(rep)
	})
	return mux
}

type uploadReport struct {
	Bytes  int64
	SHA256 string
	Err    string
}

// suiteEdge is an Edge whose Caddy has the shrunk timeouts, with a Route from
// app.test to the suite's Service.
func suiteEdge(t *testing.T) *CaddyEdge {
	t.Helper()
	return suiteEdgeFor(t, suiteService())
}

// suiteEdgeFor is suiteEdge with a Service of the test's own.
func suiteEdgeFor(t *testing.T, service http.Handler) *CaddyEdge {
	t.Helper()
	svc := NewHTTPService(t, service)
	h := New(t)
	o := h.AddOutpost(t, OutpostOptions{Name: "home"})
	to := shrunk
	ce := h.Edge.StartCaddy(t, CaddyOptions{Timeouts: &to})
	h.Edge.Routes.Swap([]routes.Route{{Hostname: "app.test", Outpost: o.Name, Service: svc.Addr()}})
	return ce
}

// sseRun is what a Visitor saw of an event stream.
type sseRun struct {
	events     int
	maxLatency time.Duration
	encoding   string // the response's Content-Encoding
	err        error
}

func readSSE(t *testing.T, ce *CaddyEdge, proto Proto, gaps string) sseRun {
	var res sseRun
	req, err := http.NewRequestWithContext(CtxFor(t, time.Minute), "GET", "https://app.test/sse?gaps="+gaps, nil)
	if err != nil {
		res.err = err
		return res
	}
	// Setting Accept-Encoding ourselves turns off Go's transparent
	// decompression, so an encoding the Edge applies shows up in the response.
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := ce.Client(t, proto).Do(req)
	if err != nil {
		res.err = err
		return res
	}
	defer resp.Body.Close()
	res.encoding = resp.Header.Get("Content-Encoding")
	if resp.Proto != string(proto) {
		res.err = fmt.Errorf("spoke %s, want %s", resp.Proto, proto)
		return res
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		f := strings.Fields(data)
		ns, _ := strconv.ParseInt(f[1], 10, 64)
		res.maxLatency = max(res.maxLatency, time.Since(time.Unix(0, ns)))
		res.events++
	}
	res.err = sc.Err()
	return res
}

// forEachProto runs f for every protocol at once.
func forEachProto[T any](f func(Proto) T) map[Proto]T {
	var mu sync.Mutex
	var wg sync.WaitGroup
	out := map[Proto]T{}
	for _, p := range Protos {
		wg.Go(func() {
			r := f(p)
			mu.Lock()
			out[p] = r
			mu.Unlock()
		})
	}
	wg.Wait()
	return out
}

func TestCaddyTimeoutsCanBeShrunk(t *testing.T) {
	// A connection that never sends a request header is cut after
	// read_header_timeout, so the time to the cut shows the shrunk value.
	ce := suiteEdge(t)
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", ce.HTTPSPort))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	start := time.Now()
	c.SetReadDeadline(start.Add(10 * time.Second))
	io.Copy(io.Discard, c)
	if d := time.Since(start); d < time.Second || d > 6*time.Second {
		t.Fatalf("a silent connection was cut after %v, want about 2 s", d)
	}
}

func TestEventsReachTheVisitorAsTheServiceWritesThem(t *testing.T) {
	ce := suiteEdge(t)
	// Gaps shorter than the timeouts: this is about buffering, not timeouts.
	res := forEachProto(func(p Proto) sseRun { return readSSE(t, ce, p, "300ms,300ms,300ms,300ms") })
	for _, p := range Protos {
		r := res[p]
		if r.err != nil || r.events != 5 {
			t.Errorf("%s: %d events, %v; want 5", p, r.events, r.err)
		}
		// Compression buffers a stream and, over a long one, defeats it.
		if r.encoding != "" {
			t.Errorf("%s: the event stream came back with Content-Encoding %q", p, r.encoding)
		}
		// Buffering would hold an event back for a gap or until the stream ends.
		if r.maxLatency > 200*time.Millisecond {
			t.Errorf("%s: an event arrived %v after it was written", p, r.maxLatency)
		}
	}
}

func TestEventStreamWithAHeartbeatOutlivesEveryIdleTimeoutAndSeveralReloads(t *testing.T) {
	ce := suiteEdge(t)
	// 3 s heartbeats are longer than read_idle, write_idle and idle (2 s each).
	// Three reloads fall between them; grace_period is 0, so none cuts a stream.
	done := make(chan map[Proto]sseRun, 1)
	go func() {
		done <- forEachProto(func(p Proto) sseRun { return readSSE(t, ce, p, "3s,3s,3s,3s") })
	}()
	for range 3 {
		time.Sleep(3 * time.Second)
		ce.Reload(t)
	}
	res := <-done
	for _, p := range Protos {
		r := res[p]
		if r.err != nil || r.events != 5 {
			t.Errorf("%s: %d of 5 events, %v", p, r.events, r.err)
		}
		if r.maxLatency > time.Second {
			t.Errorf("%s: an event arrived %v after it was written", p, r.maxLatency)
		}
	}
}

// patternReader streams size pseudo-random bytes, hashing what it hands out, so
// a body of any size is never held in memory.
type patternReader struct {
	rng  *rand.ChaCha8
	left int64
	sum  hash.Hash
	// stallAfter and stall make the reader pause once, like a slow Visitor.
	stallAfter int64
	stall      time.Duration
	sent       int64
}

func newPatternReader(size int64) *patternReader {
	var seed [32]byte
	copy(seed[:], "tnl caddy suite upload body seed")
	return &patternReader{rng: rand.NewChaCha8(seed), left: size, sum: sha256.New()}
}

func (p *patternReader) Read(b []byte) (int, error) {
	if p.left == 0 {
		return 0, io.EOF
	}
	if p.stall > 0 && p.sent >= p.stallAfter {
		time.Sleep(p.stall)
		p.stall = 0
	}
	b = b[:min(int64(len(b)), p.left)]
	if p.stall > 0 {
		b = b[:min(int64(len(b)), p.stallAfter-p.sent)]
	}
	p.rng.Read(b)
	p.sum.Write(b)
	p.left -= int64(len(b))
	p.sent += int64(len(b))
	return len(b), nil
}

func (p *patternReader) Hex() string { return hex.EncodeToString(p.sum.Sum(nil)) }

type uploadResult struct {
	status int
	report uploadReport
	err    error
	// encoding is the response's Content-Encoding.
	encoding string
	took     time.Duration
	// sha256 is what the Visitor sent.
	sha256 string
}

func (r uploadResult) intact(size int64) bool {
	return r.err == nil && r.status == 200 && r.encoding == "" && r.report.Err == "" && r.report.Bytes == size && r.report.SHA256 == r.sha256
}

func (r uploadResult) String() string {
	return fmt.Sprintf("status %d, Service reported %+v, sent SHA-256 %s, err %v, in %v", r.status, r.report, r.sha256, r.err, r.took.Round(time.Millisecond))
}

// upload POSTs size bytes of body as a Visitor on proto and reads the Service's report.
func upload(t *testing.T, ce *CaddyEdge, proto Proto, body *patternReader, size int64, query string) uploadResult {
	start := time.Now()
	var res uploadResult
	req, err := http.NewRequestWithContext(CtxFor(t, 2*time.Minute), "POST", "https://app.test/upload?"+query, body)
	if err != nil {
		res.err = err
		return res
	}
	req.ContentLength = size
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := ce.Client(t, proto).Do(req)
	if err != nil {
		res.err, res.took = err, time.Since(start)
		return res
	}
	defer resp.Body.Close()
	res.status = resp.StatusCode
	res.encoding = resp.Header.Get("Content-Encoding")
	b, err := io.ReadAll(resp.Body)
	res.took = time.Since(start)
	res.sha256 = body.Hex()
	if err != nil {
		res.err = err
		return res
	}
	json.Unmarshal(b, &res.report)
	return res
}

func TestUpload250MBReachesTheServiceIntact(t *testing.T) {
	const size = 250_000_000
	ce := suiteEdge(t)
	for _, p := range Protos {
		t.Run(string(p), func(t *testing.T) {
			r := upload(t, ce, p, newPatternReader(size), size, "")
			if !r.intact(size) {
				t.Fatalf("%s", r)
			}
			t.Logf("%.0f MB/s", size/1e6/r.took.Seconds())
		})
	}
}

func TestUploadWhoseServicePausesReadingForLessThanReadIdleCompletes(t *testing.T) {
	const size = 64_000_000
	ce := suiteEdge(t)
	// The Service stops reading for 1 s, half of the shrunk read_idle_timeout.
	// The spec raises it to 5 min so that HTTP/2 Visitors survive such pauses.
	res := forEachProto(func(p Proto) uploadResult {
		return upload(t, ce, p, newPatternReader(size), size, "stallAfter=8000000&stall=1s")
	})
	for _, p := range Protos {
		if !res[p].intact(size) {
			t.Errorf("%s: %s", p, res[p])
		}
	}
}

func TestUploadWhoseServiceStopsReadingLongerThanReadIdleFailsOnHTTP2Only(t *testing.T) {
	// Known limit (spec section 6): Go's HTTP/2 read-deadline timer fires on the
	// stalled body whatever the Service does. HTTP/1.1 and HTTP/3 wait. If this
	// test fails because HTTP/2 now survives, the limit is gone: update section 6
	// and decide whether read_idle_timeout still needs its 5 min.
	const size = 64_000_000
	ce := suiteEdge(t)
	res := forEachProto(func(p Proto) uploadResult {
		return upload(t, ce, p, newPatternReader(size), size, "stallAfter=8000000&stall=4s")
	})
	for _, p := range Protos {
		r := res[p]
		if p == H2 && r.intact(size) {
			t.Errorf("HTTP/2 survived a Service that stopped reading for 2x read_idle_timeout; the known limit is gone: %s", r)
		}
		if p != H2 && !r.intact(size) {
			t.Errorf("%s: %s", p, r)
		}
	}
}

func TestUploadWhoseVisitorStallsLongerThanReadIdleFailsOnEveryProtocol(t *testing.T) {
	// Known limit (spec section 6): read_idle_timeout also bounds how long a
	// Visitor may stall mid-upload. If this fails because an upload completed,
	// update section 6.
	const size = 16_000_000
	ce := suiteEdge(t)
	res := forEachProto(func(p Proto) uploadResult {
		body := newPatternReader(size)
		body.stallAfter, body.stall = 4_000_000, 4*time.Second
		return upload(t, ce, p, body, size, "")
	})
	for _, p := range Protos {
		if res[p].intact(size) {
			t.Errorf("%s: an upload stalled for 4 s completed: %s", p, res[p])
		}
	}
}

func TestRequestBodiesStreamToTheServiceUnbuffered(t *testing.T) {
	// The Visitor sends a first chunk and then waits, without finishing the
	// body, until the Service has seen that chunk. An Edge that buffers
	// request bodies would hold the chunk back and the wait would time out.
	seen := make(chan struct{}, 1)
	ce := suiteEdgeFor(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 16)
		if _, err := io.ReadFull(r.Body, buf); err == nil {
			select {
			case seen <- struct{}{}:
			default:
			}
		}
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(200)
	}))
	for _, p := range Protos {
		t.Run(string(p), func(t *testing.T) {
			select {
			case <-seen:
			default:
			}
			pr, pw := io.Pipe()
			req, err := http.NewRequestWithContext(CtxFor(t, 30*time.Second), "POST", "https://app.test/", pr)
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				resp, err := ce.Client(t, p).Do(req)
				if err == nil {
					resp.Body.Close()
				}
				done <- err
			}()
			go pw.Write([]byte("0123456789abcdef"))
			select {
			case <-seen:
			case <-time.After(1500 * time.Millisecond):
				pw.Close()
				t.Fatalf("the Service saw no byte of a request body the Visitor was still sending")
			}
			pw.Close()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}
