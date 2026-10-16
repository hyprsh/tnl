//go:build !outpost

package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/hyprsh/tnl/internal/edge"
)

// testWebFile is set only by scripts/check-skew.sh, with
// -ldflags "-X main.testWebFile=PATH". An Edge built that way reads its Caddy
// side (listen addresses, ports, ACME CA and its roots) from that JSON file,
// which is how the check runs real Edge binaries as an unprivileged user on high
// ports against a test CA. It is a link-time string, not a flag or a variable,
// so a release build has no such setting and no way to turn it on.
var testWebFile string

// applyTestWeb overlays the file's edge.Web fields onto w, if the build names one.
func applyTestWeb(w *edge.Web) error {
	if testWebFile == "" {
		return nil
	}
	b, err := os.ReadFile(testWebFile)
	if err != nil {
		return fmt.Errorf("test build: %w", err)
	}
	if err := json.Unmarshal(b, w); err != nil {
		return fmt.Errorf("test build: %s: %w", testWebFile, err)
	}
	return nil
}
