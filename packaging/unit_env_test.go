package packaging

import (
	"os"
	"strings"
	"testing"
)

// Caddy resolves its config directory while its package initialises, before main,
// and warns on stderr if neither HOME nor XDG_CONFIG_HOME is set. The Edge unit has
// no HOME, so it must set the variable, under its state directory.
func TestEdgeUnitSetsXDGConfigHome(t *testing.T) {
	unit, err := os.ReadFile("systemd/tnl-edge.service")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "\nEnvironment=XDG_CONFIG_HOME=/var/lib/tnl/config\n") {
		t.Errorf("tnl-edge.service does not set XDG_CONFIG_HOME under /var/lib/tnl:\n%s", unit)
	}
}
