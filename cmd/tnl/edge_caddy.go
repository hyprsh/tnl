//go:build !outpost

package main

// The Edge links Caddy; the Outpost-only build must not. scripts/check-outpost-deps.sh
// checks both. Wiring caddyx into `tnl edge` itself comes with ticket 26.
import _ "github.com/hyprsh/tnl/internal/edge/caddyx"
