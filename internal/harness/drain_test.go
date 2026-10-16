package harness

import (
	"bufio"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// drainService serves what stopping has to deal with:
//
//	/events  an event stream that writes once and then waits for the Visitor to go
//	/upload  reads the whole body and answers with its size
//	/hang    never answers
type drainService struct {
	*Service
	bodyStarted chan struct{}
}

func newDrainService(t *testing.T) *drainService {
	d := &drainService{bodyStarted: make(chan struct{}, 8)}
	mux := http.NewServeMux()
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: 0\n\n")
		http.NewResponseController(w).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		first := make([]byte, 1)
		io.ReadFull(r.Body, first)
		d.bodyStarted <- struct{}{}
		n, _ := io.Copy(io.Discard, r.Body)
		io.WriteString(w, "got "+strconv.Itoa(int(n)+1))
	})
	mux.HandleFunc("/hang", func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	d.Service = NewHTTPService(t, mux)
	return d
}

// drainEdge is a real Edge with Caddy, a Route app.test and an Outpost, with its
// log captured and the drain shortened to timeout.
func drainEdge(t *testing.T, timeout time.Duration) (*RealEdge, *drainService, *lockedBuf) {
	t.Helper()
	svc := newDrainService(t)
	logs := &lockedBuf{}
	re := StartEdgeWith(t, EdgeOptions{Pebble: StartPebble(t, PebbleOptions{}), LogOut: logs, DrainTimeout: timeout})
	re.AddZone(t, "test")
	re.JoinOutpost(t, "nas")
	re.AddRoute(t, "app.test", "http://"+svc.Addr(), "nas")
	// The first request pays for the certificate.
	get(t, re.Caddy, H1, "app.test", "/warm")
	return re, svc, logs
}

func TestSIGTERMEndsStreamsAtOnceAndLetsASlowUploadFinish(t *testing.T) {
	for _, proto := range Protos {
		t.Run(string(proto), func(t *testing.T) {
			re, svc, logs := drainEdge(t, 0)
			client := re.Caddy.Client(t, proto)

			req, _ := http.NewRequestWithContext(ctx(t), "GET", "https://app.test/events", nil)
			req.Header.Set("Accept", "text/event-stream")
			sse, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer sse.Body.Close()
			if line, _ := bufio.NewReader(sse.Body).ReadString('\n'); !strings.HasPrefix(line, "data:") {
				t.Fatalf("first event = %q", line)
			}

			pr, pw := io.Pipe()
			upReq, _ := http.NewRequestWithContext(ctx(t), "POST", "https://app.test/upload", pr)
			type result struct {
				code int
				body string
				err  error
			}
			uploaded := make(chan result, 1)
			go func() {
				resp, err := client.Do(upReq)
				if err != nil {
					uploaded <- result{err: err}
					return
				}
				defer resp.Body.Close()
				b, _ := io.ReadAll(resp.Body)
				uploaded <- result{code: resp.StatusCode, body: string(b)}
			}()
			pw.Write([]byte("hello "))
			select {
			case <-svc.bodyStarted:
			case <-time.After(10 * time.Second):
				t.Fatal("the upload never reached the Service")
			}

			stopped := make(chan struct{})
			go func() { re.Stop(); close(stopped) }()
			ended := make(chan struct{})
			go func() { io.Copy(io.Discard, sse.Body); close(ended) }()
			select {
			case <-ended:
			case <-time.After(3 * time.Second):
				t.Fatal("the event stream was not cut at once")
			}
			select {
			case <-stopped:
				t.Fatal("the Edge exited with an upload still running")
			case <-time.After(300 * time.Millisecond):
			}
			pw.Write([]byte("world"))
			pw.Close()
			select {
			case r := <-uploaded:
				if r.err != nil || r.code != 200 || r.body != "got 11" {
					t.Fatalf("the upload: %d %q %v", r.code, r.body, r.err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the upload did not finish")
			}
			select {
			case <-stopped:
			case <-time.After(5 * time.Second):
				t.Fatal("the Edge did not exit once the upload ended")
			}
			if got := logs.String(); !strings.Contains(got, "drained=1") || !strings.Contains(got, "streams_cut=1") {
				t.Errorf("the stop line does not give both counts:\n%s", got)
			}
		})
	}
}

func TestARequestStillRunningAfterTheDrainIsClosed(t *testing.T) {
	for _, proto := range Protos {
		t.Run(string(proto), func(t *testing.T) {
			re, _, logs := drainEdge(t, 300*time.Millisecond)
			client := re.Caddy.Client(t, proto)
			var wg sync.WaitGroup
			wg.Add(1)
			failed := make(chan error, 1)
			go func() {
				defer wg.Done()
				req, _ := http.NewRequestWithContext(ctx(t), "GET", "https://app.test/hang", nil)
				resp, err := client.Do(req)
				if err == nil {
					_, err = io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
					if err == nil {
						err = io.EOF // the connection ended without an error
					}
				}
				failed <- err
			}()
			time.Sleep(500 * time.Millisecond) // let the request reach the Service
			start := time.Now()
			re.Stop()
			if took := time.Since(start); took > 6*time.Second {
				t.Errorf("stopping took %v", took)
			}
			select {
			case <-failed:
			case <-time.After(5 * time.Second):
				t.Fatal("the request was not closed")
			}
			wg.Wait()
			if got := logs.String(); !strings.Contains(got, "closed=1") {
				t.Errorf("the stop line does not count the closed request:\n%s", got)
			}
		})
	}
}
