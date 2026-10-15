package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/keys"
)

type logbuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logbuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logbuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newLog() (*slog.Logger, *logbuf) {
	b := &logbuf{}
	return slog.New(slog.NewTextHandler(b, &slog.HandlerOptions{Level: slog.LevelDebug})), b
}

func mustKey(t *testing.T) keys.Key {
	t.Helper()
	_, pub, err := keys.Generate()
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func open(t *testing.T, dir string) (*Store, *logbuf) {
	t.Helper()
	lg, buf := newLog()
	s, err := Open(dir, lg)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return s, buf
}

// addSample puts a Zone, an Outpost and a Route into the state.
func addSample(t *testing.T, s *Store) {
	t.Helper()
	pub := mustKey(t)
	err := s.Update("local", func(st *State) error {
		st.Zones = append(st.Zones, Zone{Name: "example.com", Mode: PerHostname})
		st.Outposts = append(st.Outposts, Outpost{Name: "home", PublicKey: pub, Address: st.AllocAddr()})
		st.Routes = append(st.Routes, Route{Hostname: "app.example.com", Outpost: "home", Service: "http://localhost:3000"})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFirstStartCreatesStateWithKeyAndLogsFingerprint(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tnl")
	s, buf := open(t, dir)

	st := s.Snapshot()
	if st.Version != 1 || st.PrivateKey == (keys.Key{}) || st.NextAddr != 0 {
		t.Fatalf("bad new state: %+v", st)
	}
	if len(st.Outposts)+len(st.Routes)+len(st.Zones) != 0 {
		t.Fatal("new state is not empty")
	}
	fp := st.PrivateKey.Public().Fingerprint()
	log := buf.String()
	if !strings.Contains(log, "no state in "+dir+", created a new Edge") && !strings.Contains(log, `"no state in `+dir+`, created a new Edge"`) {
		t.Errorf("missing first-start line: %s", log)
	}
	if !strings.Contains(log, fp) {
		t.Errorf("log lacks fingerprint %s: %s", fp, log)
	}
	if strings.Contains(log, st.PrivateKey.Hex()) {
		t.Error("log leaks the private key")
	}
}

func TestPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tnl")
	s, _ := open(t, dir)
	addSample(t, s)
	di, _ := os.Stat(dir)
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", di.Mode().Perm())
	}
	fi, _ := os.Stat(filepath.Join(dir, "state.json"))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("stray files after save: %v", entries)
	}
}

func TestExistingEmptyDirIsAFirstStart(t *testing.T) {
	dir := t.TempDir() // exists, mode 0700 by TempDir; empty
	s, _ := open(t, dir)
	if s.Snapshot().PrivateKey == (keys.Key{}) {
		t.Fatal("no key")
	}
}

func TestNonEmptyDirWithoutStateIsRefused(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "something"), nil, 0o600)
	lg, _ := newLog()
	if _, err := Open(dir, lg); err == nil {
		t.Fatal("opened a directory that has files but no state.json")
	}
}

func TestRestartReloadsWhatWasSaved(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	addSample(t, s)
	want := s.Snapshot()

	s2, buf := open(t, dir)
	got := s2.Snapshot()
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(got)
	if string(a) != string(b) {
		t.Fatalf("reload differs:\n%s\n%s", a, b)
	}
	if strings.Contains(buf.String(), "created a new Edge") {
		t.Error("a reload claimed to create a new Edge")
	}
	if got.Outposts[0].Address != addr.Outpost(0) || got.NextAddr != 1 {
		t.Errorf("address plan: %+v", got)
	}
}

func TestOptionalOutpostFieldsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	one := mustKey(t)
	exp := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	ep := netip.MustParseAddrPort("203.0.113.5:51820")
	err := s.Update("local", func(st *State) error {
		st.Outposts = append(st.Outposts, Outpost{Name: "o", PublicKey: mustKey(t), Address: st.AllocAddr(), Endpoint: ep, OneTimeKey: &one, TokenExpires: exp})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	o := func() Outpost { s2, _ := open(t, dir); return s2.Snapshot().Outposts[0] }()
	if o.Endpoint != ep || o.OneTimeKey == nil || *o.OneTimeKey != one || !o.TokenExpires.Equal(exp) {
		t.Fatalf("lost fields: %+v", o)
	}
}

// writeState hand-writes a state.json, as an operator or a bug might.
func writeState(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func openErr(t *testing.T, dir string) (error, string) {
	t.Helper()
	lg, buf := newLog()
	_, err := Open(dir, lg)
	return err, buf.String()
}

func TestUnknownSchemaVersionIsRefused(t *testing.T) {
	for _, v := range []string{"2", "0", "99"} {
		dir := t.TempDir()
		writeState(t, dir, `{"version": `+v+`}`)
		err, _ := openErr(t, dir)
		if err == nil || !strings.Contains(err.Error(), "version") {
			t.Errorf("version %s: err=%v", v, err)
		}
	}
	// A newer file with fields this tnl has never heard of still gets the version message.
	dir := t.TempDir()
	writeState(t, dir, `{"version": 7, "shiny": true}`)
	err, _ := openErr(t, dir)
	if err == nil || !strings.Contains(err.Error(), "version 7") {
		t.Errorf("err=%v", err)
	}
}

func TestStartChecksTheWholeStateAndNamesTheBadEntry(t *testing.T) {
	good := mustKey(t)
	priv, _, _ := keys.Generate()
	k := func(x keys.Key) string { b, _ := x.MarshalText(); return string(b) }
	doc := func(outposts, routes, zones string, next int) string {
		return `{"version":1,"private_key":"` + k(priv) + `","next_address":` + itoa(next) +
			`,"outposts":[` + outposts + `],"routes":[` + routes + `],"zones":[` + zones + `]}`
	}
	op := func(name, addr string) string {
		return `{"name":"` + name + `","public_key":"` + k(good) + `","address":"` + addr + `"}`
	}
	rt := func(host, outpost string) string {
		return `{"hostname":"` + host + `","outpost":"` + outpost + `","service":"http://x:1"}`
	}
	zone := `{"name":"example.com","mode":"per-hostname"}`

	cases := []struct {
		name, body, mention string
	}{
		{"duplicate hostname", doc(op("a", "100.64.0.2"), rt("x.example.com", "a")+","+rt("x.example.com", "a"), zone, 1), "x.example.com"},
		{"route to missing outpost", doc("", rt("x.example.com", "ghost"), zone, 0), "ghost"},
		{"hostname outside every zone", doc(op("a", "100.64.0.2"), rt("x.other.org", "a"), zone, 1), "x.other.org"},
		{"duplicate tunnel address", doc(op("a", "100.64.0.2")+","+op("b", "100.64.0.2"), "", zone, 2), "100.64.0.2"},
		{"address at the counter", doc(op("a", "100.64.0.2"), "", zone, 0), `"a"`},
		{"address beyond the counter", doc(op("a", "100.64.0.9"), "", zone, 1), `"a"`},
		{"address outside the range", doc(op("a", "10.0.0.2"), "", zone, 1), `"a"`},
		{"bad outpost key", strings.Replace(doc(op("a", "100.64.0.2"), "", zone, 1), k(good), "bm90YWtleQ==", 1), `"a"`},
		{"bad private key", strings.Replace(doc("", "", zone, 0), k(priv), "nope", 1), "private_key"},
		{"wildcard zone without provider", doc("", "", `{"name":"example.com","mode":"wildcard"}`, 0), "example.com"},
		{"unknown mode", doc("", "", `{"name":"example.com","mode":"sometimes"}`, 0), "example.com"},
		{"duplicate outpost name", doc(op("a", "100.64.0.2")+","+op("a", "100.64.0.3"), "", zone, 2), `"a"`},
		{"unknown field", strings.Replace(doc("", "", zone, 0), `"version":1,`, `"version":1,"bogus":1,`, 1), "bogus"},
		{"not json", `{`, "state.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeState(t, dir, tc.body)
			err, _ := openErr(t, dir)
			if err == nil {
				t.Fatal("loaded a bad state")
			}
			if !strings.Contains(err.Error(), tc.mention) {
				t.Errorf("error %q does not name %q", err, tc.mention)
			}
		})
	}

	// The matching good document loads, so the cases above fail for their stated reason.
	dir := t.TempDir()
	writeState(t, dir, doc(op("a", "100.64.0.2"), rt("x.example.com", "a")+","+rt("example.com", "a"), zone, 1))
	if err, _ := openErr(t, dir); err != nil {
		t.Fatalf("good state refused: %v", err)
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestUpdateRejectsAnInvalidChangeAndChangesNothing(t *testing.T) {
	s, _ := open(t, t.TempDir())
	addSample(t, s)
	before := s.Snapshot()
	err := s.Update("local", func(st *State) error {
		st.Routes = append(st.Routes, Route{Hostname: "app.example.com", Outpost: "home", Service: "http://x"})
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "app.example.com") {
		t.Fatalf("err=%v", err)
	}
	if s.Snapshot() != before {
		t.Fatal("snapshot changed")
	}
	// A function error passes through untouched.
	boom := errors.New("boom")
	if err := s.Update("local", func(*State) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err=%v", err)
	}
}

func TestSnapshotsAreNotMutatedByLaterChanges(t *testing.T) {
	s, _ := open(t, t.TempDir())
	addSample(t, s)
	old := s.Snapshot()
	err := s.Update("local", func(st *State) error {
		st.Routes[0].Service = "http://changed:1"
		st.Zones = append(st.Zones, Zone{Name: "other.org", Mode: PerHostname})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if old.Routes[0].Service != "http://localhost:3000" || len(old.Zones) != 1 {
		t.Fatalf("old snapshot was mutated: %+v", old)
	}
	if s.Snapshot().Routes[0].Service != "http://changed:1" {
		t.Fatal("new snapshot lacks the change")
	}
}

func TestFailedSaveFailsTheChangeAndLeavesLiveStateAlone(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := t.TempDir()
	s, _ := open(t, dir)
	addSample(t, s)
	before := s.Snapshot()
	notified := 0
	s.OnChange(func(*State) { notified++ })

	os.Chmod(dir, 0o500)
	err := s.Update("local", func(st *State) error {
		st.Zones = append(st.Zones, Zone{Name: "other.org", Mode: PerHostname})
		return nil
	})
	os.Chmod(dir, 0o700)
	if err == nil {
		t.Fatal("save succeeded in a read-only directory")
	}
	if s.Snapshot() != before || notified != 0 {
		t.Fatal("live state changed after a failed save")
	}
	// The Edge recovers once the disk does.
	if err := s.Update("local", func(st *State) error {
		st.Zones = append(st.Zones, Zone{Name: "other.org", Mode: PerHostname})
		return nil
	}); err != nil {
		t.Fatalf("later save: %v", err)
	}
}

func TestSaveIsRefusedWhenStateJSONWasChangedOutside(t *testing.T) {
	dir := t.TempDir()
	s, buf := open(t, dir)
	addSample(t, s)
	before := s.Snapshot()

	path := filepath.Join(dir, "state.json")
	raw, _ := os.ReadFile(path)
	os.WriteFile(path, append(raw, ' '), 0o600) // any difference at all counts

	err := s.Update("local", func(st *State) error {
		st.Zones = append(st.Zones, Zone{Name: "other.org", Mode: PerHostname})
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "state.json was changed outside tnl; restart the Edge to load it") {
		t.Fatalf("err=%v", err)
	}
	if s.Snapshot() != before {
		t.Fatal("live state changed")
	}
	if !strings.Contains(buf.String(), "level=WARN") {
		t.Errorf("refusal not logged at warn: %s", buf.String())
	}

	// A deleted file counts too.
	os.Remove(path)
	if err := s.Update("local", func(*State) error { return nil }); err == nil {
		t.Fatal("saved over a deleted state.json")
	}
}

func TestReadersAreNotBlockedWhileAChangeHoldsTheLock(t *testing.T) {
	s, _ := open(t, t.TempDir())
	addSample(t, s)
	inside := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error)
	go func() {
		done <- s.Update("local", func(st *State) error {
			close(inside)
			<-release
			st.Zones = append(st.Zones, Zone{Name: "other.org", Mode: PerHostname})
			return nil
		})
	}()
	<-inside

	read := make(chan int)
	go func() { read <- len(s.Snapshot().Zones) }()
	select {
	case n := <-read:
		if n != 1 {
			t.Errorf("reader saw %d zones while the change was pending", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a reader waited for the lock")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(s.Snapshot().Zones) != 2 {
		t.Fatal("change not visible after the lock was released")
	}
}

func TestChangesAreSerialised(t *testing.T) {
	s, _ := open(t, t.TempDir())
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.Update("local", func(st *State) error {
				st.Zones = append(st.Zones, Zone{Name: "z" + itoa(i) + ".org", Mode: PerHostname})
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := len(s.Snapshot().Zones); n != 20 {
		t.Fatalf("lost updates: %d zones", n)
	}
}

func TestOnChangeGetsTheNewSnapshotAfterTheSave(t *testing.T) {
	dir := t.TempDir()
	s, _ := open(t, dir)
	var seen *State
	var onDisk []byte
	s.OnChange(func(st *State) {
		seen = st
		onDisk, _ = os.ReadFile(filepath.Join(dir, "state.json"))
	})
	addSample(t, s)
	if seen != s.Snapshot() {
		t.Fatal("subscriber got a different snapshot")
	}
	if !bytes.Contains(onDisk, []byte("app.example.com")) {
		t.Fatal("subscriber ran before the save")
	}
}

func TestAllocAddrNeverReuses(t *testing.T) {
	st := &State{}
	a, b := st.AllocAddr(), st.AllocAddr()
	if a != addr.Outpost(0) || b != addr.Outpost(1) || st.NextAddr != 2 {
		t.Fatalf("%v %v %d", a, b, st.NextAddr)
	}
}

func TestStateFileIsNotWorldReadableEvenIfUmaskIsOpen(t *testing.T) {
	old := setUmask(0)
	defer setUmask(old)
	dir := filepath.Join(t.TempDir(), "d")
	s, _ := open(t, dir)
	addSample(t, s)
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		fi, _ := d.Info()
		if fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is %v", p, fi.Mode().Perm())
		}
		return nil
	})
}
