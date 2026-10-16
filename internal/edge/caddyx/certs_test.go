package caddyx

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/caddyserver/certmagic"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Split(strings.TrimSpace(s.b.String()), "\n")
}

func certsFixture() (*Certs, *testClock, *syncBuf) {
	buf := new(syncBuf)
	lg := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == "time" {
			return slog.Attr{}
		}
		return a
	}}))
	clock := &testClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	return newCerts(func() *slog.Logger { return lg }, clock), clock, buf
}

func failed(name, msg string) certEvent { return certEvent{name: name, err: msg} }

// want fails unless exactly one line contains every fragment.
func want(t *testing.T, lines []string, frags ...string) {
	t.Helper()
	var n int
	for _, l := range lines {
		ok := true
		for _, f := range frags {
			ok = ok && strings.Contains(l, f)
		}
		if ok {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d lines contain %q, want 1, in:\n%s", n, frags, strings.Join(lines, "\n"))
	}
}

func TestReportGivesTheTablesTheStatusUnderAnyCase(t *testing.T) {
	c, clock, _ := certsFixture()
	na := clock.Now().Add(30 * 24 * time.Hour)
	c.process(certEvent{obtained: true, name: "app.test", notAfter: na})
	c.process(failed("app.test", "boom"))
	st := c.Report("APP.test.")
	if !st.NotAfter.Equal(na) || st.Failures != 1 || st.LastError != "boom" || !st.FirstFailure.Equal(clock.Now()) {
		t.Fatalf("%+v", st)
	}
	if st := c.Report("other.test"); !st.NotAfter.IsZero() || st.Failures != 0 {
		t.Fatalf("a name never heard of: %+v", st)
	}
	if c.Covered("app.test") {
		t.Fatal("covered with no wildcard held")
	}
}

func TestFirstFailureWarnsLaterAttemptsDebug(t *testing.T) {
	c, _, buf := certsFixture()
	c.process(failed("app.test", "boom"))
	c.process(failed("app.test", "boom again"))
	lines := buf.lines()
	if len(lines) != 2 {
		t.Fatalf("lines: %q", lines)
	}
	want(t, lines[:1], "level=WARN", `msg="certificate attempt failed"`, "hostname=app.test", "error=boom")
	want(t, lines[1:], "level=DEBUG", "error=\"boom again\"")
}

func TestRenewalFailureNamesTheTimeLeft(t *testing.T) {
	c, _, buf := certsFixture()
	ev := failed("app.test", "boom")
	ev.renewal, ev.remaining = true, 36*time.Hour
	c.process(ev)
	want(t, buf.lines(), "level=WARN", "time_left=36h0m0s")
}

func TestObtainedAfterFailuresSaysHowMany(t *testing.T) {
	c, clock, buf := certsFixture()
	c.process(failed("app.test", "boom"))
	c.process(failed("app.test", "boom"))
	clock.advance(time.Hour)
	c.process(certEvent{obtained: true, name: "app.test", issuer: "acme-ca", notAfter: clock.Now().Add(90 * 24 * time.Hour)})
	lines := buf.lines()
	want(t, lines, "level=INFO", `msg="certificate obtained"`, "issuer=acme-ca", "expires=2026-04-01", "after_failed_attempts=2", "failing_for=1h0m0s")
	st, _ := c.Status("app.test")
	if st.Failures != 0 || st.Obtained != 1 || st.Failed != 2 {
		t.Errorf("status %+v", st)
	}
}

func TestPlainObtainHasNoFailureFields(t *testing.T) {
	c, clock, buf := certsFixture()
	c.process(certEvent{obtained: true, renewal: true, name: "app.test", issuer: "acme-ca", notAfter: clock.Now().Add(time.Hour)})
	lines := buf.lines()
	want(t, lines, "level=INFO", `msg="certificate renewed"`)
	if strings.Contains(lines[0], "after_failed_attempts") {
		t.Errorf("unexpected failure field: %s", lines[0])
	}
}

func TestDailyReminderWarnsThenErrorsOnceExpired(t *testing.T) {
	c, clock, buf := certsFixture()
	c.process(certEvent{obtained: true, name: "app.test", issuer: "ca", notAfter: clock.Now().Add(30 * time.Hour)})
	c.process(failed("app.test", "boom"))
	c.process(failed("app.test", "boom"))

	clock.advance(23 * time.Hour)
	c.Tick()
	for _, l := range buf.lines() {
		if strings.Contains(l, "still failing") {
			t.Fatalf("reminder before a day: %s", l)
		}
	}
	clock.advance(time.Hour)
	c.Tick()
	want(t, buf.lines(), "level=WARN", `msg="certificate still failing"`, "failed_attempts=2", "time_left=6h0m0s")

	clock.advance(24 * time.Hour) // the certificate expired 18 h ago
	c.Tick()
	want(t, buf.lines(), "level=ERROR", `msg="certificate still failing"`, "expired_for=18h0m0s")
}

func TestEventsRecordedFromTheHandlerAreProcessedOffThatGoroutine(t *testing.T) {
	c, _, buf := certsFixture()
	c.record(failed("app.test", "boom"))
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st, ok := c.Status("app.test"); ok && st.Failed == 1 {
			want(t, buf.lines(), `msg="certificate attempt failed"`)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the recorded event was never processed")
}

func TestObtainWaitsForTheCertificateEvent(t *testing.T) {
	c, clock, _ := certsFixture()
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		done <- c.Wait(ctx, "app.test")
	}()
	select {
	case err := <-done:
		t.Fatalf("Wait returned before a certificate: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	c.record(certEvent{obtained: true, name: "app.test", issuer: "ca", notAfter: clock.Now().Add(time.Hour)})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWaitTimeoutNamesTheLastError(t *testing.T) {
	c, _, _ := certsFixture()
	c.process(failed("app.test", "the CA said no"))
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err := c.Wait(ctx, "app.test")
	if err == nil || !strings.Contains(err.Error(), "the CA said no") {
		t.Fatalf("got %v", err)
	}
}

// A certificate that expired while the Edge was down has no NotAfter in the
// manager's memory, but it is in Caddy's storage, and the reminder reads it
// there: once it has expired the reminder is an error.
func TestAnExpiredCertificateFromBeforeARestartEscalatesToError(t *testing.T) {
	c, clock, buf := certsFixture()
	dir := t.TempDir()
	c.storage.Store(&certmagic.FileStorage{Path: dir})

	writeCert(t, dir, "app.test", clock.Now().Add(-10*time.Hour))

	c.process(failed("app.test", "boom"))
	clock.advance(24 * time.Hour)
	c.Tick()
	want(t, buf.lines(), "level=ERROR", `msg="certificate still failing"`, "expired_for=")
}

// writeCert stores a certificate for the name the way Caddy does, under any
// issuer.
func writeCert(t *testing.T, root, name string, notAfter time.Time) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: notAfter.Add(-90 * 24 * time.Hour), NotAfter: notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dir := certDir(name)
	crtDir := filepath.Join(root, "certificates", "acme-v02.api.letsencrypt.org-directory", dir)
	if err := os.MkdirAll(crtDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(crtDir, dir+".crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A certificate already expired at start, with a failure being reminded of but
// no NotAfter in memory (no failure event fills it), escalates on the reminder.
func TestTheReminderReadsAnExpiredCertificatesExpiryFromStorage(t *testing.T) {
	c, clock, buf := certsFixture()
	dir := t.TempDir()
	c.storage.Store(&certmagic.FileStorage{Path: dir})
	writeCert(t, dir, "app.test", clock.Now().Add(-10*time.Hour))
	c.mu.Lock()
	c.statusFor("app.test").LastError = "boom"
	c.mu.Unlock()
	c.failing.Begin("app.test")
	clock.advance(24 * time.Hour)
	c.Tick()
	want(t, buf.lines(), "level=ERROR", `msg="certificate still failing"`, "expired_for=")
}

// A wildcard certificate in storage counts as held before Caddy has loaded, so a
// restart orders nothing for the Hostnames it covers.
func TestAWildcardInStorageCoversItsHostnamesBeforeCaddyLoads(t *testing.T) {
	c, clock, _ := certsFixture()
	dir := t.TempDir()
	c.storage.Store(&certmagic.FileStorage{Path: dir})
	writeCert(t, dir, "*.z.test", clock.Now().Add(30*24*time.Hour))
	c.SetWildcards("z.test")
	if err := c.Retain("a.z.test", "z.test", "deep.a.z.test"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.names["a.z.test"]; ok {
		t.Error("a covered Hostname was handed over")
	}
	if _, ok := c.names["z.test"]; ok {
		t.Error("the apex was handed over")
	}
	if _, ok := c.names["deep.a.z.test"]; !ok {
		t.Error("a deeper name is not covered and needs its own")
	}
}

// The apex's own certificate does not make the wildcard held.
func TestAnApexCertificateAloneDoesNotHoldTheWildcard(t *testing.T) {
	c, clock, _ := certsFixture()
	dir := t.TempDir()
	c.storage.Store(&certmagic.FileStorage{Path: dir})
	writeCert(t, dir, "z.test", clock.Now().Add(30*24*time.Hour))
	c.SetWildcards("z.test")
	if got := c.heldWildcards(); len(got) != 0 {
		t.Fatalf("held %v", got)
	}
	if err := c.Retain("a.z.test"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.names["a.z.test"]; !ok {
		t.Error("a.z.test must keep its own certificate until the wildcard is in")
	}
}

// The apex's certificate arriving after the wildcard's settles at once too.
func TestTheApexArrivingLastSettlesTheCoveredHostnames(t *testing.T) {
	c, clock, _ := certsFixture()
	dir := t.TempDir()
	c.storage.Store(&certmagic.FileStorage{Path: dir})
	c.SetWildcards("z.test")
	if err := c.Retain("a.z.test"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.names["a.z.test"]; !ok {
		t.Fatal("not handed over")
	}
	writeCert(t, dir, "*.z.test", clock.Now().Add(30*24*time.Hour))
	c.process(certEvent{obtained: true, name: "z.test", notAfter: clock.Now().Add(30 * 24 * time.Hour)})
	if _, ok := c.names["a.z.test"]; ok {
		t.Error("a.z.test still renews its own certificate")
	}
}
