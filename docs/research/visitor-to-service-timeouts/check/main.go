// Command check is a throwaway check for hyprsh/tnl#10 (timeouts and
// buffering from visitor to Service). It is research code, not tnl code.
//
// It runs everything in one process on loopback:
//
//   - a Service: plain HTTP on 127.0.0.1, serving an SSE stream and an
//     upload sink;
//   - an Outpost: a wireguard-go netstack device that accepts Tunnel TCP
//     connections, reads a one-line header naming the Service, dials it
//     and copies bytes both ways;
//   - an Edge: a second netstack device plus embedded Caddy (caddy.Load),
//     whose reverse_proxy uses a small RoundTripper module that dials the
//     Outpost through the netstack.
//
// Caddy's server timeouts are shrunk (2 s instead of 1-5 min) so that
// pauses longer than every timeout fit into seconds. Every stream here
// lasts at most about a minute.
//
// Run: go run .          (all steps, about 3 minutes)
//
//	go run . quic     (step 6 only)
//
// Built and run with Go 1.27.2; with Go 1.26, HTTP/2 would come from
// golang.org/x/net's own copy instead of net/http.
package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	_ "github.com/caddyserver/caddy/v2/modules/standard"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

var (
	edgeNet     *netstack.Net
	outpostAddr = netip.MustParseAddrPort("100.64.0.2:7000")
)

// ---------------------------------------------------------------- Service

func startService() string {
	mux := http.NewServeMux()

	// /sse?gaps=3s,3s,3s writes one event, then waits each gap and writes
	// the next. Each event carries its sequence number and write time.
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			io.Copy(io.Discard, r.Body)
		}
		var gaps []time.Duration
		for _, g := range strings.Split(r.URL.Query().Get("gaps"), ",") {
			if d, err := time.ParseDuration(g); err == nil {
				gaps = append(gaps, d)
			}
		}
		rc := http.NewResponseController(w)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
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

	// /upload?stallAfter=N&stall=4s reads the body, optionally stops
	// reading for a while after N bytes, and reports the byte count.
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		stallAfter, _ := strconv.ParseInt(r.URL.Query().Get("stallAfter"), 10, 64)
		stall, _ := time.ParseDuration(r.URL.Query().Get("stall"))
		var n int64
		if stallAfter > 0 {
			m, _ := io.CopyN(io.Discard, r.Body, stallAfter)
			n += m
			time.Sleep(stall)
		}
		m, err := io.Copy(io.Discard, r.Body)
		n += m
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		json.NewEncoder(w).Encode(map[string]any{"bytes": n, "err": msg})
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	go http.Serve(ln, mux)
	return ln.Addr().String()
}

// ---------------------------------------------------------------- Tunnel

func keypair() (priv, pub [32]byte) {
	rand.Read(priv[:])
	priv[0] &= 248
	priv[31] = (priv[31] & 127) | 64
	p, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	must(err)
	copy(pub[:], p)
	return
}

func startTunnel() {
	edgePriv, edgePub := keypair()
	outPriv, outPub := keypair()

	etun, enet, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("100.64.0.1")}, nil, 1420)
	must(err)
	edev := device.NewDevice(etun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "edge: "))
	must(edev.IpcSet(fmt.Sprintf("private_key=%s\nlisten_port=0\npublic_key=%s\nallowed_ip=100.64.0.2/32\n",
		hex.EncodeToString(edgePriv[:]), hex.EncodeToString(outPub[:]))))
	must(edev.Up())
	cfg, err := edev.IpcGet()
	must(err)
	var port string
	for _, l := range strings.Split(cfg, "\n") {
		if p, ok := strings.CutPrefix(l, "listen_port="); ok {
			port = p
		}
	}
	edgeNet = enet

	otun, onet, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("100.64.0.2")}, nil, 1420)
	must(err)
	odev := device.NewDevice(otun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelError, "outpost: "))
	must(odev.IpcSet(fmt.Sprintf("private_key=%s\npublic_key=%s\nendpoint=127.0.0.1:%s\nallowed_ip=100.64.0.1/32\npersistent_keepalive_interval=25\n",
		hex.EncodeToString(outPriv[:]), hex.EncodeToString(edgePub[:]), port)))
	must(odev.Up())

	ln, err := onet.ListenTCP(&net.TCPAddr{Port: 7000})
	must(err)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go outpostConn(c)
		}
	}()

	// wait for the first handshake
	for range 100 {
		s, _ := edev.IpcGet()
		if strings.Contains(s, "last_handshake_time_sec=") && !strings.Contains(s, "last_handshake_time_sec=0\n") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	panic("no WireGuard handshake")
}

// outpostConn is the Outpost's whole job for one Tunnel connection: read the
// tnl header with a deadline, dial the Service, reply, then copy bytes both
// ways with no deadlines at all.
func outpostConn(tc net.Conn) {
	defer tc.Close()
	tc.SetReadDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReader(tc)
	line, err := br.ReadString('\n')
	if err != nil {
		return
	}
	tc.SetReadDeadline(time.Time{})
	target, ok := strings.CutPrefix(strings.TrimSpace(line), "TNL1 ")
	if !ok {
		return
	}
	svc, err := (&net.Dialer{Timeout: 5 * time.Second}).Dial("tcp", target)
	if err != nil {
		fmt.Fprintf(tc, "ERR %v\n", err)
		return
	}
	defer svc.Close()
	io.WriteString(tc, "OK\n")
	var wg sync.WaitGroup
	wg.Go(func() {
		io.Copy(svc, br)
		svc.(*net.TCPConn).CloseWrite()
	})
	io.Copy(tc, svc)
	tc.(interface{ CloseWrite() error }).CloseWrite()
	wg.Wait()
}

// ---------------------------------------------------------------- Edge transport module

type TnlTransport struct {
	Service string `json:"service"`
	rt      *http.Transport
}

func (TnlTransport) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "http.reverse_proxy.transport.tnlcheck", New: func() caddy.Module { return new(TnlTransport) }}
}

func (t *TnlTransport) Provision(caddy.Context) error {
	var p http.Protocols
	p.SetHTTP1(true)
	t.rt = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			c, err := edgeNet.DialContextTCPAddrPort(ctx, outpostAddr)
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(c, "TNL1 %s\n", t.Service)
			var reply []byte
			b := make([]byte, 1)
			for {
				if _, err := c.Read(b); err != nil {
					c.Close()
					return nil, err
				}
				if b[0] == '\n' {
					break
				}
				reply = append(reply, b[0])
			}
			if string(reply) != "OK" {
				c.Close()
				return nil, errors.New(string(reply))
			}
			return c, nil
		},
		Protocols:           &p,
		DisableCompression:  true,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
		// No ResponseHeaderTimeout, no ExpectContinueTimeout, no Client.Timeout.
	}
	return nil
}

func (t *TnlTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.URL.Scheme = "http"
	return t.rt.RoundTrip(r)
}

// Cleanup runs when a reload unloads the old config, while its requests may
// still be streaming. It must only drop idle connections.
func (t *TnlTransport) Cleanup() error {
	t.rt.CloseIdleConnections()
	return nil
}

func init() { caddy.RegisterModule(TnlTransport{}) }

// ---------------------------------------------------------------- Caddy config

var (
	httpsPort int
	svcAddr   string
	tmpDir    string
)

func caddyConfig(grace string) []byte {
	proxy := map[string]any{
		"handler":   "reverse_proxy",
		"transport": map[string]any{"protocol": "tnlcheck", "service": svcAddr},
		"upstreams": []any{map[string]any{"dial": outpostAddr.String()}},
	}
	cfg := map[string]any{
		"admin":   map[string]any{"disabled": true, "config": map[string]any{"persist": false}},
		"storage": map[string]any{"module": "file_system", "root": filepath.Join(tmpDir, "storage")},
		"logging": map[string]any{"logs": map[string]any{"default": map[string]any{
			"level":  "INFO",
			"writer": map[string]any{"output": "file", "filename": filepath.Join(tmpDir, "caddy.log")},
		}}},
		"apps": map[string]any{
			"http": map[string]any{
				"grace_period": grace,
				"http_port":    httpsPort + 1,
				"https_port":   httpsPort,
				"servers": map[string]any{"edge": map[string]any{
					"listen":              []string{fmt.Sprintf(":%d", httpsPort)},
					"read_header_timeout": "2s",
					"idle_timeout":        "2s",
					"read_idle_timeout":   "2s",
					"write_idle_timeout":  "2s",
					"automatic_https":     map[string]any{"disable_redirects": true},
					"routes": []any{
						map[string]any{
							"match":  []any{map[string]any{"host": []string{"app.test"}}},
							"handle": []any{proxy},
						},
						map[string]any{
							"match": []any{map[string]any{"host": []string{"enc.test"}}},
							"handle": []any{
								map[string]any{"handler": "encode", "encodings": map[string]any{"gzip": map[string]any{}}, "prefer": []string{"gzip"}},
								proxy,
							},
						},
					},
				}},
			},
			"tls": map[string]any{"automation": map[string]any{"policies": []any{map[string]any{
				"subjects": []string{"app.test", "enc.test"},
				"issuers":  []any{map[string]any{"module": "internal"}},
			}}}},
			"pki": map[string]any{"certificate_authorities": map[string]any{"local": map[string]any{"install_trust": false}}},
		},
	}
	b, err := json.Marshal(cfg)
	must(err)
	return b
}

// ---------------------------------------------------------------- clients

func client(proto, host string) http.RoundTripper {
	tlsCfg := &tls.Config{InsecureSkipVerify: true, ServerName: host}
	edge := fmt.Sprintf("127.0.0.1:%d", httpsPort)
	switch proto {
	case "h3", "h3-noping":
		tlsCfg.NextProtos = []string{http3.NextProtoH3}
		t := &http3.Transport{
			TLSClientConfig: tlsCfg,
			Dial: func(ctx context.Context, _ string, tc *tls.Config, qc *quic.Config) (*quic.Conn, error) {
				return quic.DialAddrEarly(ctx, edge, tc, qc)
			},
		}
		if proto == "h3-noping" {
			// quic-go's HTTP/3 client sends a PING every 10 s by default;
			// switch that off to see the server's idle timeout alone.
			t.QUICConfig = &quic.Config{MaxIncomingStreams: -1}
		}
		return t
	default:
		var p http.Protocols
		if proto == "h2" {
			p.SetHTTP2(true)
		} else {
			p.SetHTTP1(true)
		}
		return &http.Transport{
			TLSClientConfig: tlsCfg,
			Protocols:       &p,
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, edge)
			},
		}
	}
}

func url(host, path string) string { return fmt.Sprintf("https://%s:%d%s", host, httpsPort, path) }

type sseResult struct {
	name     string
	got      int
	want     int
	maxLat   time.Duration
	lasted   time.Duration
	proto    string
	encoding string
	end      string
}

func (r sseResult) String() string {
	return fmt.Sprintf("%-34s %-8s events %2d/%-2d  max latency %-9v  lasted %-6v  enc=%-4s end: %s",
		r.name, r.proto, r.got, r.want, r.maxLat.Round(100*time.Microsecond), r.lasted.Round(100*time.Millisecond), r.encoding, r.end)
}

func runSSE(name, proto, host, method, gaps string, want int) sseResult {
	res := sseResult{name: name, want: want}
	start := time.Now()
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(strings.Repeat("x", 1024))
	}
	req, _ := http.NewRequest(method, url(host, "/sse?gaps="+gaps), body)
	if host == "enc.test" {
		req.Header.Set("Accept-Encoding", "gzip")
	}
	resp, err := client(proto, host).RoundTrip(req)
	if err != nil {
		res.end = "request error: " + err.Error()
		return res
	}
	defer resp.Body.Close()
	res.proto = resp.Proto
	if proto == "h3-noping" {
		res.proto += "*"
	}
	res.encoding = resp.Header.Get("Content-Encoding")
	var rd io.Reader = resp.Body
	if res.encoding == "gzip" {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			res.end = "gzip: " + err.Error()
			return res
		}
		rd = gz
	}
	sc := bufio.NewScanner(rd)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}
		f := strings.Fields(data)
		ns, _ := strconv.ParseInt(f[1], 10, 64)
		lat := time.Since(time.Unix(0, ns))
		res.maxLat = max(res.maxLat, lat)
		res.got++
	}
	res.lasted = time.Since(start)
	if err := sc.Err(); err != nil {
		res.end = err.Error()
	} else {
		res.end = "EOF"
	}
	return res
}

func runUpload(name, proto string, size int64, query string) string {
	start := time.Now()
	req, _ := http.NewRequest(http.MethodPost, url("app.test", "/upload?"+query), io.LimitReader(zeros{}, size))
	req.ContentLength = size
	resp, err := client(proto, "app.test").RoundTrip(req)
	if err != nil {
		return fmt.Sprintf("%-34s %-3s request error after %v: %v", name, proto, time.Since(start).Round(100*time.Millisecond), err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	d := time.Since(start)
	return fmt.Sprintf("%-34s %-3s status %d in %-6v (%.0f MB/s)  service saw: %s",
		name, proto, resp.StatusCode, d.Round(100*time.Millisecond), float64(size)/1e6/d.Seconds(), strings.TrimSpace(string(b)))
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

// ---------------------------------------------------------------- main

func parallel[T any](fs ...func() T) []T {
	out := make([]T, len(fs))
	var wg sync.WaitGroup
	for i, f := range fs {
		wg.Go(func() { out[i] = f() })
	}
	wg.Wait()
	return out
}

func show[T fmt.Stringer](rs []T) {
	for _, r := range rs {
		fmt.Println("  ", r)
	}
}

var protos = []string{"h1", "h2", "h3"}

func main() {
	var err error
	tmpDir, err = os.MkdirTemp("", "tnl10-")
	must(err)
	fmt.Println("caddy log:", filepath.Join(tmpDir, "caddy.log"))

	svcAddr = startService()
	startTunnel()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	httpsPort = l.Addr().(*net.TCPAddr).Port
	l.Close()

	must(caddy.Load(caddyConfig("0s"), true))
	time.Sleep(time.Second) // internal CA and certificates

	if len(os.Args) > 1 && os.Args[1] == "quic" {
		quicIdle()
		return
	}

	fmt.Println("\n1. SSE with 3 s pauses; read_idle, write_idle and idle timeouts are 2 s")
	var fs []func() sseResult
	for _, p := range protos {
		fs = append(fs,
			func() sseResult { return runSSE("GET, 4 gaps of 3s", p, "app.test", "GET", "3s,3s,3s,3s", 5) },
			func() sseResult {
				return runSSE("POST with 1 KiB body, 4 gaps of 3s", p, "app.test", "POST", "3s,3s,3s,3s", 5)
			},
			func() sseResult {
				return runSSE("GET through encode, 4 gaps of 3s", p, "enc.test", "GET", "3s,3s,3s,3s", 5)
			},
		)
	}
	show(parallel(fs...))

	fmt.Println("\n2. 250 MB uploads, one at a time")
	for _, p := range protos {
		fmt.Println("  ", runUpload("250 MB", p, 250e6, ""))
	}

	fmt.Println("\n3. 64 MB upload, Service stops reading for 4 s after 8 MB (read_idle 2 s)")
	for _, p := range protos {
		fmt.Println("  ", runUpload("64 MB, 4 s stall", p, 64e6, "stallAfter=8000000&stall=4s"))
	}

	fmt.Println("\n4. SSE (1 event/s for 10 s) across two reloads at t=3s and t=6s, grace_period 0 (eternal)")
	fs = nil
	for _, p := range protos {
		fs = append(fs, func() sseResult {
			return runSSE("GET, reloads, eternal grace", p, "app.test", "GET", strings.Repeat("1s,", 9)+"1s", 11)
		})
	}
	go func() {
		time.Sleep(3 * time.Second)
		must(caddy.Load(caddyConfig("0s"), true))
		time.Sleep(3 * time.Second)
		must(caddy.Load(caddyConfig("0s"), true))
	}()
	show(parallel(fs...))

	fmt.Println("\n5. SSE (1 event/s for 12 s) across one reload at t=2s; the old config has grace_period 3s")
	must(caddy.Load(caddyConfig("3s"), true))
	time.Sleep(time.Second)
	fs = nil
	for _, p := range protos {
		fs = append(fs, func() sseResult {
			return runSSE("GET, reload, grace 3s", p, "app.test", "GET", strings.Repeat("1s,", 11)+"1s", 13)
		})
	}
	go func() {
		time.Sleep(2 * time.Second)
		must(caddy.Load(caddyConfig("0s"), true))
	}()
	show(parallel(fs...))

	quicIdle()
	must(caddy.Stop())
}

func quicIdle() {
	fmt.Println("\n6. QUIC idle timeout (30 s): 20 s heartbeats vs. one 35 s pause (* = client PINGs off)")
	var fs []func() sseResult
	for _, p := range append(protos, "h3-noping") {
		fs = append(fs,
			func() sseResult { return runSSE("GET, 3 gaps of 20s", p, "app.test", "GET", "20s,20s,20s", 4) },
			func() sseResult { return runSSE("GET, 1 gap of 35s", p, "app.test", "GET", "35s", 2) },
		)
	}
	show(parallel(fs...))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
