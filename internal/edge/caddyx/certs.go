package caddyx

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/modules/caddytls"

	"github.com/hyprsh/tnl/internal/dnsname"
	"github.com/hyprsh/tnl/internal/remind"
)

// tickEvery is how often the manager checks whether a reminder is owed.
const tickEvery = time.Minute

var errNotLoaded = errors.New("caddy has not loaded a config yet")

// CertStatus is what the manager knows about one Hostname's certificate. It is
// kept in memory and never saved; a restart starts it afresh, filled in from
// the certificates Caddy loads.
type CertStatus struct {
	Name string
	// Issuer and NotAfter describe the certificate last obtained or seen; NotAfter
	// is zero until the manager has seen one.
	Issuer   string
	NotAfter time.Time
	// Failures counts failed attempts since the last success.
	Failures     int
	FirstFailure time.Time
	LastError    string
	// Obtained and Failed count the events received, over the process's life.
	Obtained, Failed int
}

// certEvent is one cert_obtained or cert_failed event, reduced to what the
// manager records.
type certEvent struct {
	obtained  bool
	renewal   bool
	name      string
	issuer    string
	certPath  string        // storage key of the certificate, for the expiry
	notAfter  time.Time     // if zero, read from certPath
	remaining time.Duration // of the old certificate, on a failed renewal
	err       string
}

// Certs is the certificate manager (spec, The Edge). It hands every Route
// Hostname to Caddy's TLS app under a policy that isn't on-demand, so
// certmagic renews it in the background, subscribes to the certificate events,
// and turns them into tnl's log lines. Its state outlives Caddy reloads, so it
// lives in the Runtime.
type Certs struct {
	log   func() *slog.Logger
	clock remind.Clock

	mu      sync.Mutex
	issuer  json.RawMessage
	names   map[string]struct{} // every Hostname handed over
	wild    []string            // the wildcard Zones, whose certificates cover their Hostnames
	applied map[string]struct{} // those given a policy in app
	app     *caddytls.TLS
	status  map[string]*CertStatus
	changed chan struct{}
	failing *remind.Tracker

	// queue holds events from the handler, which only records and returns.
	qmu      sync.Mutex
	queue    []certEvent
	draining bool

	stopOnce sync.Once
	started  bool
	stop     chan struct{}
}

func newCerts(log func() *slog.Logger, clock remind.Clock) *Certs {
	if clock == nil {
		clock = remind.SystemClock{}
	}
	return &Certs{
		log: log, clock: clock,
		names: map[string]struct{}{}, applied: map[string]struct{}{},
		status:  map[string]*CertStatus{},
		changed: make(chan struct{}),
		failing: remind.NewTracker(remind.Daily, clock),
		stop:    make(chan struct{}),
	}
}

func normalize(name string) string { return strings.ToLower(strings.TrimSuffix(name, ".")) }

// Manage hands the Hostnames to Caddy, which begins obtaining their
// certificates in the background and keeps them renewed. It is idempotent and
// does not wait. Before Caddy has loaded it only remembers the names, and the
// load hands them over.
func (c *Certs) Manage(names ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, n := range names {
		if n = normalize(n); !c.coveredLocked(n) {
			c.names[n] = struct{}{}
		}
	}
	err := c.apply()
	if errors.Is(err, errNotLoaded) {
		return nil
	}
	return err
}

// apply gives the names not yet handed to the live TLS app a policy that isn't
// on-demand and starts managing them. The caller holds c.mu.
func (c *Certs) apply() error {
	app := activeTLS()
	if app == nil {
		return errNotLoaded
	}
	if app != c.app {
		// A reload makes a new TLS app, which knows none of this.
		c.app, c.applied = app, map[string]struct{}{}
	}
	var fresh []string
	set := map[string]struct{}{}
	for n := range c.names {
		if _, ok := c.applied[n]; !ok {
			fresh = append(fresh, n)
			set[n] = struct{}{}
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	// A policy of its own, ahead of the catch-all that stays as the on-demand
	// fallback: certmagic's maintenance loop skips every certificate whose
	// policy is on-demand.
	ap := &caddytls.AutomationPolicy{SubjectsRaw: fresh}
	if len(c.issuer) > 0 {
		ap.IssuersRaw = []json.RawMessage{c.issuer}
	}
	if err := app.AddAutomationPolicy(ap); err != nil {
		return fmt.Errorf("certificate policy for %v: %w", fresh, err)
	}
	if err := app.Manage(set); err != nil {
		return err
	}
	for _, n := range fresh {
		c.applied[n] = struct{}{}
	}
	return nil
}

// Retain makes the given Hostnames exactly the ones the manager keeps: it
// manages those not yet handed over, and stops renewing the certificates of
// every other Hostname it had. The Edge calls it with every Route's Hostname
// after each change to the state, so a removed Route, however it went, stops
// renewing.
func (c *Certs) Retain(names ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	want := make(map[string]struct{}, len(names))
	for _, n := range names {
		if n = normalize(n); !c.coveredLocked(n) {
			want[n] = struct{}{}
		}
	}
	var drop []string
	for n := range c.names {
		if _, ok := want[n]; !ok {
			drop = append(drop, n)
		}
	}
	for n := range want {
		c.names[n] = struct{}{}
	}
	err := c.stopLocked(drop)
	if aerr := c.apply(); err == nil && !errors.Is(aerr, errNotLoaded) {
		err = aerr
	}
	return err
}

// SetWildcards sets the wildcard Zones. A Hostname one label below one, or its
// own name, is covered by the Zone's wildcard certificate and gets none of its
// own; a deeper name is not covered and does. It affects what Manage and
// Retain take from then on, so the Edge calls it before Retain.
func (c *Certs) SetWildcards(zones ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.wild = make([]string, len(zones))
	for i, z := range zones {
		c.wild[i] = normalize(z)
	}
}

func (c *Certs) coveredLocked(name string) bool { return dnsname.WildcardCovers(name, c.wild) }

// Stop stops renewing the Hostnames' certificates and forgets them. A
// certificate already held keeps serving until it expires; nothing asks the CA
// for another, and a handshake for the name no longer reaches it on demand.
func (c *Certs) Stop(names ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopLocked(names)
}

// stopLocked is Stop with c.mu held.
//
// Caddy has no call to stop managing a name: its certificate cache is private,
// and a reload would close every WebSocket. certmagic's maintenance loop skips
// every certificate whose config is on-demand, though, and Caddy picks a
// certificate's config by name from the first matching automation policy. So
// the names get a policy of their own, ahead of the one that manages them,
// that is on-demand.
func (c *Certs) stopLocked(names []string) error {
	var applied []string
	for _, n := range names {
		n = normalize(n)
		delete(c.names, n)
		delete(c.status, n)
		c.failing.End(n)
		if _, ok := c.applied[n]; ok {
			applied = append(applied, n)
			delete(c.applied, n)
		}
	}
	if len(applied) == 0 || c.app == nil || activeTLS() != c.app {
		return nil
	}
	ap := &caddytls.AutomationPolicy{SubjectsRaw: applied, OnDemand: true}
	if len(c.issuer) > 0 {
		ap.IssuersRaw = []json.RawMessage{c.issuer}
	}
	if err := c.app.AddAutomationPolicy(ap); err != nil {
		return fmt.Errorf("stop renewing %v: %w", applied, err)
	}
	return nil
}

func activeTLS() *caddytls.TLS {
	app, err := caddy.ActiveContext().AppIfConfigured("tls")
	if err != nil {
		return nil
	}
	t, _ := app.(*caddytls.TLS)
	return t
}

// Obtain manages the Hostname and waits until a certificate is held for it, or
// ctx ends. The error then says what Caddy last reported; Caddy keeps trying.
func (c *Certs) Obtain(ctx context.Context, name string) error {
	if err := c.Manage(name); err != nil {
		return err
	}
	return c.Wait(ctx, name)
}

// Wait waits until a certificate is held for the Hostname, or ctx ends.
func (c *Certs) Wait(ctx context.Context, name string) error {
	name = normalize(name)
	for {
		c.mu.Lock()
		ch := c.changed
		c.mu.Unlock()
		if c.holds(name) {
			return nil
		}
		select {
		case <-ctx.Done():
			st, _ := c.Status(name)
			if st.LastError != "" {
				return fmt.Errorf("%w (last error: %s)", ctx.Err(), st.LastError)
			}
			return ctx.Err()
		case <-ch:
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// holds says whether an unexpired certificate for the name is held: one the
// manager saw obtained, or one in Caddy's cache (loaded from storage).
func (c *Certs) holds(name string) bool {
	now := c.clock.Now()
	c.mu.Lock()
	st := c.status[name]
	ok := st != nil && st.NotAfter.After(now)
	c.mu.Unlock()
	// Caddy announces "obtained" before the certificate is in its cache, and a
	// visitor who arrives in between makes Caddy obtain it again on demand and
	// fail with a TLS internal error. With Caddy running, the cache decides.
	if ok && activeTLS() == nil {
		return true
	}
	if na, issuer, found := cachedCert(name); found && na.After(now) {
		c.mu.Lock()
		c.statusFor(name).NotAfter, c.statusFor(name).Issuer = na, issuer
		c.mu.Unlock()
		return true
	}
	return false
}

// cachedCert finds the unexpired certificate in Caddy's cache that covers name,
// and says when it expires.
func cachedCert(name string) (notAfter time.Time, issuer string, found bool) {
	if activeTLS() == nil {
		return
	}
	defer func() {
		if recover() != nil { // the cache goes away while Caddy stops
			found = false
		}
	}()
	for _, cert := range caddytls.AllMatchingCertificates(name) {
		if cert.Leaf == nil || cert.Expired() {
			continue
		}
		if cert.Leaf.NotAfter.After(notAfter) {
			notAfter, issuer, found = cert.Leaf.NotAfter, cert.Leaf.Issuer.CommonName, true
		}
	}
	return
}

// Managing says how many Hostnames have been handed to Caddy.
func (c *Certs) Managing() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.names)
}

// Status is what the manager knows of the Hostname's certificate.
func (c *Certs) Status(name string) (CertStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.status[normalize(name)]
	if !ok {
		return CertStatus{}, false
	}
	return *st, true
}

// Statuses lists every Hostname the manager has heard of, in no order.
func (c *Certs) Statuses() []CertStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]CertStatus, 0, len(c.status))
	for _, st := range c.status {
		out = append(out, *st)
	}
	return out
}

func (c *Certs) statusFor(name string) *CertStatus {
	st, ok := c.status[name]
	if !ok {
		st = &CertStatus{Name: name}
		c.status[name] = st
	}
	return st
}

// record is what the event handler calls: it queues the event and returns. The
// handler runs on the goroutine that emits the event, which may hold a lock
// certmagic needs, so no I/O and no logging happen here.
func (c *Certs) record(ev certEvent) {
	c.qmu.Lock()
	c.queue = append(c.queue, ev)
	start := !c.draining
	c.draining = true
	c.qmu.Unlock()
	if start {
		go c.drain()
	}
}

func (c *Certs) drain() {
	for {
		c.qmu.Lock()
		if len(c.queue) == 0 {
			c.draining = false
			c.qmu.Unlock()
			return
		}
		ev := c.queue[0]
		c.queue = c.queue[1:]
		c.qmu.Unlock()
		c.process(ev)
	}
}

// process applies one event: it updates the status and writes the log line.
func (c *Certs) process(ev certEvent) {
	name := normalize(ev.name)
	if ev.obtained && ev.notAfter.IsZero() {
		ev.notAfter = loadNotAfter(ev.certPath)
	}
	now := c.clock.Now()

	c.mu.Lock()
	st := c.statusFor(name)
	log := c.log()
	if ev.obtained {
		st.Obtained++
		failures := st.Failures
		st.Issuer, st.NotAfter = ev.issuer, ev.notAfter
		st.Failures, st.FirstFailure, st.LastError = 0, time.Time{}, ""
		since, was := c.failing.End(name)
		c.broadcast()
		c.mu.Unlock()

		msg := "certificate obtained"
		if ev.renewal {
			msg = "certificate renewed"
		}
		attrs := []any{"hostname", name, "issuer", ev.issuer, "expires", ev.notAfter.UTC().Format(time.RFC3339)}
		if failures > 0 {
			attrs = append(attrs, "after_failed_attempts", failures)
		}
		if was {
			attrs = append(attrs, "failing_for", since.Round(time.Second))
		}
		log.Info(msg, attrs...)
		return
	}

	st.Failed++
	st.Failures++
	st.LastError = ev.err
	if st.Failures == 1 {
		st.FirstFailure = now
	}
	started := c.failing.Begin(name)
	failures := st.Failures
	c.broadcast()
	c.mu.Unlock()

	attrs := []any{"hostname", name, "error", ev.err, "failed_attempts", failures}
	if ev.renewal && ev.remaining > 0 {
		attrs = append(attrs, "time_left", ev.remaining.Round(time.Second))
	}
	if started {
		log.Warn("certificate attempt failed", attrs...)
	} else {
		log.Debug("certificate attempt failed", attrs...)
	}
}

func (c *Certs) broadcast() {
	close(c.changed)
	c.changed = make(chan struct{})
}

// loadNotAfter reads the certificate at a storage key and returns its expiry,
// or the zero time.
func loadNotAfter(key string) time.Time {
	if key == "" || caddy.DefaultStorage == nil {
		return time.Time{}
	}
	b, err := caddy.DefaultStorage.Load(context.Background(), key)
	if err != nil {
		return time.Time{}
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return time.Time{}
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}
	}
	return leaf.NotAfter
}

// Tick logs the reminders that are owed: a daily warn for a certificate that
// keeps failing, and error once that certificate has expired. The manager's own
// goroutine calls it every minute; tests call it with their clock moved on.
func (c *Certs) Tick() {
	now := c.clock.Now()
	for _, r := range c.failing.Due() {
		c.mu.Lock()
		st := *c.statusFor(r.Key)
		log := c.log()
		c.mu.Unlock()
		attrs := []any{"hostname", r.Key, "error", st.LastError, "failed_attempts", st.Failures, "failing_for", r.Since.Round(time.Second)}
		if !st.NotAfter.IsZero() && !st.NotAfter.After(now) {
			log.Error("certificate still failing", append(attrs, "expired_for", now.Sub(st.NotAfter).Round(time.Second))...)
			continue
		}
		if !st.NotAfter.IsZero() {
			attrs = append(attrs, "time_left", st.NotAfter.Sub(now).Round(time.Second))
		}
		log.Warn("certificate still failing", attrs...)
	}
}

// start runs the reminder ticker, once.
func (c *Certs) start() {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return
	}
	c.started = true
	c.mu.Unlock()
	go func() {
		t := time.NewTicker(tickEvery)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				c.Tick()
			case <-c.stop:
				return
			}
		}
	}()
}

func (c *Certs) close() { c.stopOnce.Do(func() { close(c.stop) }) }

// setIssuer sets the issuer module JSON that Hostname policies use.
func (c *Certs) setIssuer(raw json.RawMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.issuer = raw
}

// loaded runs after every Caddy load: the new TLS app knows none of the names.
func (c *Certs) loaded() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.apply()
}

// EventHandler is the events.handlers.tnl module. It subscribes to
// cert_obtained and cert_failed, and only records: it runs on the goroutine
// that emits the event.
type EventHandler struct {
	Edge string `json:"edge"`

	rt *Runtime
}

// CaddyModule implements caddy.Module.
func (EventHandler) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "events.handlers.tnl", New: func() caddy.Module { return new(EventHandler) }}
}

// Provision implements caddy.Provisioner.
func (h *EventHandler) Provision(caddy.Context) (err error) {
	h.rt, err = lookupRuntime(h.Edge)
	return err
}

// Handle implements caddyevents.Handler. It never fails: an error would abort
// the event for the emitter.
func (h *EventHandler) Handle(_ context.Context, e caddy.Event) error {
	name, _ := e.Data["identifier"].(string)
	if name == "" {
		return nil
	}
	ev := certEvent{name: name}
	ev.renewal, _ = e.Data["renewal"].(bool)
	ev.remaining, _ = e.Data["remaining"].(time.Duration)
	switch e.Name() {
	case "cert_obtained":
		ev.obtained = true
		ev.issuer, _ = e.Data["issuer"].(string)
		ev.certPath, _ = e.Data["certificate_path"].(string)
	case "cert_failed":
		if err, ok := e.Data["error"].(error); ok {
			ev.err = err.Error()
		} else {
			ev.err = fmt.Sprint(e.Data["error"])
		}
	default:
		return nil
	}
	h.rt.Certs().record(ev)
	return nil
}

var _ caddy.Provisioner = (*EventHandler)(nil)
