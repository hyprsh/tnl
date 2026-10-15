//go:build !outpost

package main

// The Edge links Caddy through internal/edge (edge.Run starts it, built from
// the state and the flags). The Outpost-only build must not link it;
// scripts/check-outpost-deps.sh checks both.
