package harness

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/state"
	"github.com/hyprsh/tnl/internal/version"
)

// The skew check runs real tnl binaries of different versions against each
// other. scripts/check-skew.sh builds them and sets these variables; without
// them the tests skip, so a plain `go test ./...` builds nothing.
//
//	TNL_SKEW_WEB            the path the Edge builds were told to read (main.testWebFile)
//	TNL_SKEW_DIR            holds the stamped builds tnl-a (0.1.0), tnl-b (0.1.9), tnl-c (0.2.0)
//	TNL_SKEW_TAG_BIN        the latest release tag's build
//	TNL_SKEW_TAG_VERSION    that tag's version, without the v
//	TNL_SKEW_CUR_BIN        the current tree, stamped as release-please gives it after the tag (scripts/next-release.sh)
//	TNL_SKEW_CUR_IN_WINDOW  "1" when that stamp is the tag's next patch, inside its window; "0" when a breaking commit made it the next minor, outside
//	TNL_SKEW_CUR_OUT_BIN    the current tree, stamped outside the tag's window (a new 0.x minor; two majors on from 1.0)
//	TNL_SKEW_TAG_EDGE_OK    "1" when the tag's Edge can be put on high ports

// skewStep is one Edge and Outpost binary pair.
type skewStep struct{ edge, outpost string }

func skewEnv(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Skipf("%s is not set: run scripts/check-skew.sh", name)
	}
	return v
}

// The same minor serves a Route whichever side is newer; a different minor,
// reached by upgrading one side after they have joined, makes the Outpost
// Incompatible, whichever side is upgraded.
func TestSkewStampedBuilds(t *testing.T) {
	dir := skewEnv(t, "TNL_SKEW_DIR")
	a, b, c := filepath.Join(dir, "tnl-a"), filepath.Join(dir, "tnl-b"), filepath.Join(dir, "tnl-c")
	for _, tc := range []struct {
		name  string
		join  skewStep
		then  *skewStep
		serve bool
	}{
		{"Edge 0.1.0 with Outpost 0.1.9", skewStep{a, b}, nil, true},
		{"Edge 0.1.9 with Outpost 0.1.0", skewStep{b, a}, nil, true},
		{"Outpost upgraded to 0.2.0 under Edge 0.1.0", skewStep{a, b}, &skewStep{a, c}, false},
		{"Edge upgraded to 0.2.0 over Outpost 0.1.0", skewStep{a, a}, &skewStep{c, a}, false},
	} {
		t.Run(tc.name, func(t *testing.T) { runSkew(t, tc.join, tc.then, tc.serve) })
	}
}

// The latest release tag's build and the current tree serve a Route in both
// directions when the current tree is stamped as the tag's next patch. A
// breaking commit since the tag makes it the next minor instead: then the two
// cannot join, and only the outside-window expectation runs. Stamped outside
// the tag's window, the Outpost shows as Incompatible with either side
// upgraded to it.
func TestSkewAgainstTheLatestTag(t *testing.T) {
	tag := skewEnv(t, "TNL_SKEW_TAG_BIN")
	edgeOK := os.Getenv("TNL_SKEW_TAG_EDGE_OK") == "1"
	inside := os.Getenv("TNL_SKEW_CUR_IN_WINDOW") == "1"
	if inside {
		cur := skewEnv(t, "TNL_SKEW_CUR_BIN")
		t.Run("tag Outpost on the current Edge", func(t *testing.T) { runSkew(t, skewStep{cur, tag}, nil, true) })
		t.Run("tag Edge with the current Outpost", func(t *testing.T) {
			if !edgeOK {
				t.Skip("the tag's Edge cannot be put on high ports: its source predates the check's hook")
			}
			runSkew(t, skewStep{tag, cur}, nil, true)
		})
	}
	out := skewEnv(t, "TNL_SKEW_CUR_OUT_BIN")
	t.Run("the Outpost upgraded outside the window under the tag's Edge", func(t *testing.T) {
		if !edgeOK {
			t.Skip("the tag's Edge cannot be put on high ports: its source predates the check's hook")
		}
		// A tag before 0.2 predates the Edge judging a ping it refuses: it drops
		// the ping of a tree that has a field it lacks (the session id), never
		// learns the Outpost's version and keeps serving it, which is its
		// documented limit. From 0.2 on the Edge reads the version of a ping it
		// refuses, so the Outpost is Incompatible and answers 503, as the spec says.
		serve := true
		if v, ok := version.Parse(skewEnv(t, "TNL_SKEW_TAG_VERSION")); ok && (v.Major > 0 || v.Minor >= 2) {
			serve = false
		}
		runSkew(t, skewStep{tag, tag}, &skewStep{tag, out}, serve)
	})
	t.Run("the Edge upgraded outside the window over the tag's Outpost", func(t *testing.T) {
		// Joining needs the same minor: the current tree joins only inside the window.
		join := skewStep{tag, tag}
		if inside {
			join.edge = skewEnv(t, "TNL_SKEW_CUR_BIN")
		}
		runSkew(t, join, &skewStep{out, tag}, false)
	})
}

type skewProc struct {
	cmd  *exec.Cmd
	out  *LockedBuf
	done chan struct{}
}

func startSkewProc(t *testing.T, env []string, bin string, args ...string) *skewProc {
	t.Helper()
	p := &skewProc{out: new(LockedBuf), done: make(chan struct{})}
	p.cmd = exec.Command(bin, args...)
	p.cmd.Env = env
	p.cmd.Stdout, p.cmd.Stderr = p.out, p.out
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.cmd.Wait(); close(p.done) }()
	t.Cleanup(p.stop)
	return p
}

// stop sends SIGTERM and waits, killing after 25 s.
func (p *skewProc) stop() {
	select {
	case <-p.done:
		return
	default:
	}
	p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(25 * time.Second):
		p.cmd.Process.Kill()
		<-p.done
	}
}

// skewCLI runs a tnl command against a state directory and returns its output.
func skewCLI(bin, stateDir string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TNL_STATE_DIR=" + stateDir}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

func runSkew(t *testing.T, join skewStep, then *skewStep, serve bool) {
	webFile := skewEnv(t, "TNL_SKEW_WEB")
	pebble := StartPebble(t, PebbleOptions{})
	svc := NewHTTPService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "ok") }))

	httpPort, httpsPort, wgPort := freePort(t, false), freePort(t, true), freePort(t, true)
	web, _ := json.Marshal(map[string]any{
		"HTTPListen": fmt.Sprintf("127.0.0.1:%d", httpPort), "HTTPSListen": fmt.Sprintf("127.0.0.1:%d", httpsPort),
		"HTTPPort": httpPort, "HTTPSPort": httpsPort,
		"ACMECA": pebble.DirectoryURL, "ACMEEmail": "ops@example.com", "ACMETrustedRoots": []string{pebble.certFile},
		"DNSPropagationTimeout": -1,
	})
	if err := os.WriteFile(webFile, web, 0o600); err != nil {
		t.Fatal(err)
	}

	short, err := os.MkdirTemp("", "tnlk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(short) })
	edgeDir, outDir := filepath.Join(short, "e"), filepath.Join(short, "o")
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + short, "XDG_DATA_HOME=" + filepath.Join(short, "caddy")}

	waitSocket := func(p *skewProc, dir, what string) {
		WaitFor(t, what, 30*time.Second, func() bool {
			select {
			case <-p.done:
				t.Fatalf("%s exited:\n%s", what, p.out)
			default:
			}
			_, err := os.Stat(filepath.Join(dir, "tnl.sock"))
			return err == nil
		})
	}
	startEdge := func(bin string) *skewProc {
		p := startSkewProc(t, env, bin, "edge", "--address", "127.0.0.1", "--state-dir", edgeDir, "--wireguard-port", fmt.Sprint(wgPort), "--log-level", "info")
		waitSocket(p, edgeDir, "the Edge's socket")
		return p
	}
	startOutpost := func(bin string, args ...string) *skewProc {
		p := startSkewProc(t, env, bin, append([]string{"connect", "--state-dir", outDir, "--log-level", "info"}, args...)...)
		waitSocket(p, outDir, "the Outpost's socket")
		return p
	}
	cli := func(bin, dir string, args ...string) string {
		t.Helper()
		var out string
		var err error
		for range 50 { // the socket can be there a moment before it answers
			if out, err = skewCLI(bin, dir, args...); err == nil {
				return out
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("tnl %s: %v\n%s", strings.Join(args, " "), err, out)
		return ""
	}

	edgeProc := startEdge(join.edge)
	cli(join.edge, edgeDir, "zone", "add", "test")
	var tok string
	for _, line := range strings.Split(cli(join.edge, edgeDir, "outpost", "add", "nas"), "\n") {
		if strings.HasPrefix(line, "tnl") { // the token; a warning may share the output
			tok = line
			break
		}
	}
	if tok == "" {
		t.Fatal("no join token in the output of outpost add")
	}
	outProc := startOutpost(join.outpost, "--token", tok)
	cli(join.edge, edgeDir, "route", "add", "app.test", "http://"+svc.Addr(), "--outpost", "nas")

	roots := x509.NewCertPool()
	roots.AddCert(pebble.Root)
	visitor := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: roots},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", httpsPort))
		},
	}}
	status := func() (int, string) {
		resp, err := visitor.Get("https://app.test/")
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	waitStatus := func(want int) {
		t.Helper()
		var code int
		var got string
		deadline := time.Now().Add(45 * time.Second)
		for time.Now().Before(deadline) {
			if code, got = status(); code == want {
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
		t.Fatalf("a Visitor got %d %q, want %d\nEdge:\n%s\nOutpost:\n%s", code, got, want, edgeProc.out, outProc.out)
	}
	waitStatus(200)

	if then == nil {
		return
	}
	// Upgrade the side that differs, the Edge or the Outpost.
	if then.edge != join.edge {
		outProc.stop()
		edgeProc.stop()
		bumpEdgeSchema(t, edgeDir)
		edgeProc = startEdge(then.edge)
		outProc = startOutpost(then.outpost)
	} else if then.outpost != join.outpost {
		outProc.stop()
		outProc = startOutpost(then.outpost)
	}
	if serve {
		waitStatus(200)
		return
	}
	waitStatus(503)
	// The Outpost's own CLI goes through the Edge and is refused.
	WaitFor(t, "the Edge to refuse the Outpost's API call", 30*time.Second, func() bool {
		out, err := skewCLI(then.outpost, outDir, "route", "ls")
		return err != nil && strings.Contains(out, "doesn't accept this Outpost")
	})
	if !strings.Contains(edgeProc.out.String(), "outpost is incompatible") {
		t.Errorf("the Edge did not warn:\n%s", edgeProc.out)
	}
}

// bumpEdgeSchema stamps the stopped Edge's state.json with this tree's schema
// version. A tnl of the next minor refuses the file a tag's Edge wrote, with no
// migration before 1.0 (spec section 5), so a real upgrade would start a new
// Edge; the fields the schemas share are the same, and this test is about the
// Outpost's version, not the file.
func bumpEdgeSchema(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "state.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["version"] = state.Version
	raw, _ = json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
