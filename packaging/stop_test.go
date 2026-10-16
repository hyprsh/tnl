package packaging

import (
	"os"
	"strings"
	"testing"
)

// The Edge drains for up to 10 s on SIGTERM, so whatever stops it must wait
// longer: 20 s.
func TestEdgeStopTimeoutsOutlastTheDrain(t *testing.T) {
	for file, want := range map[string]string{
		"systemd/tnl-edge.service":      "TimeoutStopSec=20s",
		"../examples/compose/edge.yaml": "stop_grace_period: 20s",
	} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), want) {
			t.Errorf("%s lacks %q", file, want)
		}
	}
}
