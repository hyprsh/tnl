// Package packaging holds tnl's release files (units, Dockerfiles) and tests
// them: the units against the spec, install.sh against a local release.
package packaging

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const version = "1.2.3"

// The units in the repo are the spec's code fences, line for line.
func TestUnitsMatchSpec(t *testing.T) {
	spec, err := os.ReadFile("../docs/spec/v1.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tnl-edge", "tnl-outpost"} {
		re := regexp.MustCompile("(?s)```ini\n# " + name + `\.service\n(.*?)` + "```")
		m := re.FindSubmatch(spec)
		if m == nil {
			t.Fatalf("%s: no code fence in the spec", name)
		}
		got, err := os.ReadFile("systemd/" + name + ".service")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(m[1]) {
			t.Errorf("%s.service differs from the spec:\n%s", name, got)
		}
	}
}

func fakeBinary(build string) []byte {
	return []byte("#!/bin/sh\necho \"tnl " + version + " " + build + "\"\n")
}

func archive(t *testing.T, build string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name string, mode int64, data []byte) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		tw.Write(data)
	}
	add("tnl", 0o755, fakeBinary(build))
	add("LICENSE", 0o644, []byte("license"))
	add("NOTICE", 0o644, []byte("notice"))
	add("README.md", 0o644, []byte("readme"))
	for _, u := range []string{"tnl-edge", "tnl-outpost"} {
		unit, err := os.ReadFile("systemd/" + u + ".service")
		if err != nil {
			t.Fatal(err)
		}
		add("systemd/"+u+".service", 0o644, unit)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func newRelease(t *testing.T, corrupt bool) string {
	t.Helper()
	dir := t.TempDir()
	var sums strings.Builder
	for _, a := range []struct{ prefix, build string }{{"tnl", "full"}, {"tnl-outpost", "outpost"}} {
		name := fmt.Sprintf("%s_%s_linux_amd64.tar.gz", a.prefix, version)
		data := archive(t, a.build)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(data))
		if corrupt {
			sum = strings.Repeat("0", 64)
		}
		fmt.Fprintf(&sums, "%s  %s\n", sum, name)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// host is a fake machine: a root prefix, a home and a systemctl that records calls.
type host struct {
	t       *testing.T
	root    string
	home    string
	release string
	env     []string
}

func newHost(t *testing.T, corrupt bool) *host {
	t.Helper()
	h := &host{t: t, root: t.TempDir(), home: t.TempDir(), release: newRelease(t, corrupt)}
	bin := t.TempDir()
	ctl := filepath.Join(bin, "systemctl")
	script := `#!/bin/sh
echo "$*" >> "$TNL_TEST_STATE/systemctl.log"
case "$1" in
is-active) grep -qx "${3:-$2}" "$TNL_TEST_STATE/active" 2>/dev/null ;;
enable) echo "$3" >> "$TNL_TEST_STATE/active" ;;
esac
`
	if err := os.WriteFile(ctl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// A sysctl that logs its calls and reports the buffer limit in
	// $TNL_TEST_STATE/rmem_max (the stock 212992 when that file is absent).
	sysctl := `#!/bin/sh
echo "$*" >> "$TNL_TEST_STATE/sysctl.log"
case "$1" in
-n) cat "$TNL_TEST_STATE/${2##*.}" 2>/dev/null || echo 212992 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "sysctl"), []byte(sysctl), 0o755); err != nil {
		t.Fatal(err)
	}
	h.env = []string{
		"HOME=" + h.home,
		"PATH=" + bin + ":" + os.Getenv("PATH"),
		"TNL_TEST_STATE=" + h.root,
		"TNL_RELEASE_DIR=" + h.release,
		"TNL_VERSION=" + version,
		"TNL_PLATFORM=linux_amd64",
		"TNL_ROOT=" + h.root,
	}
	return h
}

func (h *host) run(mode string, args ...string) (string, string, error) {
	h.t.Helper()
	cmd := exec.Command("sh", append([]string{"../install.sh"}, args...)...)
	cmd.Env = append(h.env, "TNL_MODE="+mode)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

func (h *host) path(p string) string { return filepath.Join(h.root, p) }

func (h *host) read(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(b)
}

func TestInstallRefusesChecksumMismatch(t *testing.T) {
	h := newHost(t, true)
	_, stderr, err := h.run("user", "outpost", "--token", "tok")
	if err == nil {
		t.Fatal("install succeeded with a bad checksum")
	}
	if !strings.Contains(stderr, "checksum") {
		t.Errorf("stderr does not name the checksum: %q", stderr)
	}
	if _, err := os.Stat(filepath.Join(h.home, ".local/bin/tnl")); err == nil {
		t.Error("a binary was installed anyway")
	}
}

func TestInstallWithoutSystemdOrRootPutsTheBinaryInLocalBin(t *testing.T) {
	h := newHost(t, false)
	stdout, stderr, err := h.run("user", "outpost", "--token", "tok123")
	if err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	bin := filepath.Join(h.home, ".local/bin/tnl")
	if got := h.read(bin); !strings.Contains(got, "outpost") {
		t.Errorf("not the Outpost build: %q", got)
	}
	if !strings.Contains(stdout, "tnl connect --token tok123") {
		t.Errorf("stdout lacks the connect line: %q", stdout)
	}
	if _, err := os.Stat(h.path("etc/tnl")); err == nil {
		t.Error("user mode wrote /etc/tnl")
	}
	if _, err := os.Stat(h.path("systemctl.log")); err == nil {
		t.Error("user mode called systemctl")
	}
}

func TestInstallEdgeNeedsAnAddress(t *testing.T) {
	h := newHost(t, false)
	if _, _, err := h.run("system", "edge"); err == nil {
		t.Fatal("edge install without --address succeeded")
	}
}

func TestSystemInstallEdge(t *testing.T) {
	h := newHost(t, false)
	_, stderr, err := h.run("system", "edge", "--address", "edge.example.com")
	if err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	if got := h.read(h.path("usr/local/bin/tnl")); !strings.Contains(got, "full") {
		t.Errorf("Edge must get the full build: %q", got)
	}
	unit, _ := os.ReadFile("systemd/tnl-edge.service")
	if h.read(h.path("etc/systemd/system/tnl-edge.service")) != string(unit) {
		t.Error("unit not installed from the archive")
	}
	if got := h.read(h.path("etc/tnl/edge.env")); got != "TNL_ADDRESS=edge.example.com\n" {
		t.Errorf("edge.env = %q", got)
	}
	log := h.read(h.path("systemctl.log"))
	for _, want := range []string{"daemon-reload", "enable --now tnl-edge"} {
		if !strings.Contains(log, want) {
			t.Errorf("systemctl log lacks %q:\n%s", want, log)
		}
	}
}

func TestInstallNeverOverwritesAnEnvFile(t *testing.T) {
	h := newHost(t, false)
	if err := os.MkdirAll(h.path("etc/tnl"), 0o755); err != nil {
		t.Fatal(err)
	}
	envFile := h.path("etc/tnl/outpost.env")
	if err := os.WriteFile(envFile, []byte("TNL_TOKEN=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := h.run("system", "outpost", "--token", "new"); err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	if got := h.read(envFile); got != "TNL_TOKEN=old\n" {
		t.Errorf("env file overwritten: %q", got)
	}
}

func TestRerunningUpgradesAndRestartsActiveUnits(t *testing.T) {
	h := newHost(t, false)
	if _, stderr, err := h.run("system", "edge", "--address", "e.example.com"); err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	os.Remove(h.path("systemctl.log"))
	if err := os.WriteFile(h.path("usr/local/bin/tnl"), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := h.run("system", "edge", "--address", "e.example.com"); err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	if got := h.read(h.path("usr/local/bin/tnl")); !strings.Contains(got, "full") {
		t.Errorf("binary not upgraded: %q", got)
	}
	if log := h.read(h.path("systemctl.log")); !strings.Contains(log, "restart tnl-edge") {
		t.Errorf("active unit not restarted:\n%s", log)
	}
}

func TestOutpostInstallNeverReplacesAFullBuild(t *testing.T) {
	h := newHost(t, false)
	if _, stderr, err := h.run("system", "edge", "--address", "e.example.com"); err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	if _, stderr, err := h.run("system", "outpost", "--token", "t"); err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	if got := h.read(h.path("usr/local/bin/tnl")); !strings.Contains(got, "full") {
		t.Errorf("full build replaced: %q", got)
	}
}

// A release's install.sh has its version baked in; the repo's has a placeholder.
func TestStampedInstallScriptKnowsItsVersion(t *testing.T) {
	h := newHost(t, false)
	stamped := filepath.Join(t.TempDir(), "install.sh")
	if out, err := exec.Command("sh", "../scripts/stamp-install.sh", "v"+version, stamped).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	cmd := exec.Command("sh", stamped, "outpost", "--token", "x")
	for _, e := range h.env {
		if !strings.HasPrefix(e, "TNL_VERSION=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "TNL_MODE=user")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	// And the unstamped script refuses to guess.
	h2 := newHost(t, false)
	cmd = exec.Command("sh", "../install.sh", "outpost", "--token", "x")
	for _, e := range h2.env {
		if !strings.HasPrefix(e, "TNL_VERSION=") {
			cmd.Env = append(cmd.Env, e)
		}
	}
	cmd.Env = append(cmd.Env, "TNL_MODE=user")
	if err := cmd.Run(); err == nil {
		t.Error("unstamped install.sh ran without TNL_VERSION")
	}
}

// Only a plain X.Y.Z takes the moving tags; any other version is refused, so a
// pre-release can never move X.Y, X or latest.
func TestImageTagsAreXYZXYXAndLatestForAPlainVersionOnly(t *testing.T) {
	tags := func(v string) (string, error) {
		out, err := exec.Command("sh", "../scripts/build-images.sh", v, t.TempDir(), "tags").Output()
		return strings.TrimSpace(string(out)), err
	}
	for _, v := range []string{"v1.2.3", "1.2.3"} {
		if got, err := tags(v); err != nil || got != "1.2.3 1.2 1 latest" {
			t.Errorf("%s: %q, %v", v, got, err)
		}
	}
	for _, v := range []string{"v1.2.3-rc.1", "1.2.3-rc", "v1.2", "v1.2.3.4", "vX.Y.Z", ""} {
		if got, err := tags(v); err == nil {
			t.Errorf("%q was accepted: %q", v, got)
		}
	}
}

const sysctlFile = "etc/sysctl.d/60-tnl.conf"
const sysctlWant = "net.core.rmem_max = 7500000\nnet.core.wmem_max = 7500000\n"

// quic-go and wireguard-go ask for 7 MiB socket buffers, which Linux caps at
// net.core.rmem_max (about 208 KiB by default).
func TestEdgeInstallRaisesTheSocketBufferLimits(t *testing.T) {
	h := newHost(t, false)
	stdout, stderr, err := h.run("system", "edge", "--address", "e.example.com")
	if err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	if got := h.read(h.path(sysctlFile)); got != sysctlWant {
		t.Errorf("sysctl file = %q", got)
	}
	if log := h.read(h.path("sysctl.log")); !strings.Contains(log, "-p "+h.path(sysctlFile)) {
		t.Errorf("the file was not applied:\n%s", log)
	}
	if !strings.Contains(stdout, "net.core.rmem_max") {
		t.Errorf("stdout does not say what was set: %q", stdout)
	}
}

func TestInstallKeepsAnExistingSysctlFile(t *testing.T) {
	h := newHost(t, false)
	if err := os.MkdirAll(h.path("etc/sysctl.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.path(sysctlFile), []byte("net.core.rmem_max = 9999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := h.run("system", "edge", "--address", "e.example.com")
	if err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	if got := h.read(h.path(sysctlFile)); got != "net.core.rmem_max = 9999999\n" {
		t.Errorf("sysctl file overwritten: %q", got)
	}
	if !strings.Contains(stdout, "Kept the existing") {
		t.Errorf("stdout does not say it kept the file: %q", stdout)
	}
}

func TestOutpostInstallRaisesTheLimitsOnlyWhenTheyAreLow(t *testing.T) {
	h := newHost(t, false)
	if _, stderr, err := h.run("system", "outpost", "--token", "t"); err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	if got := h.read(h.path(sysctlFile)); got != sysctlWant {
		t.Errorf("sysctl file = %q", got)
	}

	h = newHost(t, false)
	for _, n := range []string{"rmem_max", "wmem_max"} {
		if err := os.WriteFile(h.path(n), []byte("8388608\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, err := h.run("system", "outpost", "--token", "t"); err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	if _, err := os.Stat(h.path(sysctlFile)); err == nil {
		t.Error("the limits were already high enough, yet a sysctl file was written")
	}
}

func TestUserInstallLeavesSysctlAlone(t *testing.T) {
	h := newHost(t, false)
	if _, stderr, err := h.run("user", "outpost", "--token", "t"); err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	if _, err := os.Stat(h.path("sysctl.log")); err == nil {
		t.Error("user mode called sysctl")
	}
}

func TestAFailingSysctlDoesNotFailTheInstall(t *testing.T) {
	h := newHost(t, false)
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "sysctl"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.env = append(h.env, "PATH="+bad+":"+h.env[1][len("PATH="):])
	stdout, stderr, err := h.run("system", "edge", "--address", "e.example.com")
	if err != nil {
		t.Fatalf("%v: %s", err, stderr)
	}
	if !strings.Contains(stdout+stderr, "could not apply") {
		t.Errorf("no warning: %q %q", stdout, stderr)
	}
}
