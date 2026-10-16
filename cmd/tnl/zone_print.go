//go:build !outpost

package main

import (
	"fmt"
	"strings"

	"github.com/hyprsh/tnl/internal/api"
)

// printZoneResult prints what `zone add` and `zone edit` say after a change:
// the DNS zone, the headline, the names a wildcard certificate covers, and the
// warnings.
func printZoneResult(headline string, w api.WildcardInfo, warnings api.Warnings) {
	if w.DNSZone != "" {
		fmt.Fprintf(stdout, "DNS zone %s\n", w.DNSZone)
	}
	fmt.Fprintln(stdout, headline)
	if len(w.Covers) > 0 {
		fmt.Fprintf(stdout, "the certificate covers %s\n", strings.Join(w.Covers, ", "))
	}
	printWarnings(warnings)
}

// zoneMode is a Zone's mode, with its provider if it has one.
func zoneMode(z api.ZoneInfo) string {
	if z.Provider != "" {
		return fmt.Sprintf("%s, %s", z.Mode, z.Provider)
	}
	return z.Mode
}
