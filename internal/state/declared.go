package state

import (
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/hyprsh/tnl/internal/addr"
	"github.com/hyprsh/tnl/internal/keys"
)

// DeclaredZone is a Zone line of the Edge's settings, checked.
type DeclaredZone struct {
	Name     string
	Provider string // empty for a per-Hostname Zone
	Token    string // wildcard only; never logged
}

// DeclaredOutpost is an Outpost line of the Edge's settings, checked.
type DeclaredOutpost struct {
	Name string
	Key  keys.Key // the Outpost's WireGuard public key
}

// Event is one thing the diff did, for the Edge to log after the change is saved.
type Event struct {
	Level slog.Level
	Msg   string
	Attrs []any
}

// DeclaredResult is what ApplyDeclared did.
type DeclaredResult struct {
	Events []Event
	// Checks are the declared Zones that are new or whose mode, provider or token
	// changed: they get the checks of `zone add` after the lock.
	Checks []Zone
	// Removed holds the Hostnames of the Routes that were deleted with a removed
	// Zone or Outpost, and Waiting those of declared Routes sent back to waiting.
	Removed, Waiting []string
}

func (r *DeclaredResult) log(level slog.Level, msg string, attrs ...any) {
	r.Events = append(r.Events, Event{level, msg, attrs})
}

// ApplyDeclared brings the Zones and Outposts to the Edge's settings, as a diff
// by name. It runs inside one Store.Update, at start. A kind with no lines hands
// its declared items back to the CLI and removes nothing. It returns an error,
// and the caller's Update then changes nothing, when a removed Zone would leave
// a Route made with the CLI outside every Zone.
func (s *State) ApplyDeclared(zones []DeclaredZone, outposts []DeclaredOutpost) (DeclaredResult, error) {
	var res DeclaredResult
	if err := s.applyZones(zones, &res); err != nil {
		return res, err
	}
	s.applyOutposts(outposts, &res)
	return res, nil
}

func (s *State) applyZones(want []DeclaredZone, res *DeclaredResult) error {
	if len(want) == 0 {
		var names []string
		for i := range s.Zones {
			if s.Zones[i].Declared {
				s.Zones[i].Declared = false
				names = append(names, s.Zones[i].Name)
			}
		}
		if len(names) > 0 {
			res.log(slog.LevelWarn, "no TNL_ZONE_* is set, so the declared Zones stay and the CLI manages them again; remove one with `tnl zone rm`", "zones", strings.Join(names, ","))
		}
		return nil
	}

	old := s.Clone()
	wanted := map[string]DeclaredZone{}
	for _, d := range want {
		wanted[d.Name] = d
	}

	// Check first, so a refusal leaves the state as it was: no removed Zone may
	// strand a Route made with the CLI.
	final := &State{Zones: zonesOf(want)}
	var problems []string
	for _, z := range old.Zones {
		if _, keep := wanted[z.Name]; keep {
			continue
		}
		var cli []string
		for _, r := range old.Routes {
			if in, ok := old.ZoneOf(r.Hostname); ok && in.Name == z.Name && !final.InZone(r.Hostname) && !old.Declares(r.Outpost) {
				cli = append(cli, r.Hostname)
			}
		}
		if len(cli) > 0 {
			slices.Sort(cli)
			problems = append(problems, fmt.Sprintf("%s is no longer declared, but %s %s in it; remove them or declare the Zone again",
				z.Name, strings.Join(cli, ", "), plural(len(cli), "is", "are")))
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}

	// Removals first: a Zone line that's gone is `zone rm --with-routes`.
	for _, z := range slices.Clone(s.Zones) {
		if _, keep := wanted[z.Name]; keep {
			continue
		}
		gone := s.RemoveZone(z.Name)
		res.Removed = append(res.Removed, gone.Removed...)
		res.Waiting = append(res.Waiting, gone.Waiting...)
		res.log(slog.LevelWarn, "zone removed: it is no longer declared", "zone", z.Name, "routes_removed", len(gone.Removed), "routes_waiting", len(gone.Waiting))
	}

	for _, d := range want {
		nz := Zone{Name: d.Name, Mode: PerHostname, Declared: true}
		if d.Provider != "" {
			nz = Zone{Name: d.Name, Mode: Wildcard, Provider: d.Provider, Token: d.Token, Declared: true}
		}
		i := slices.IndexFunc(s.Zones, func(z Zone) bool { return z.Name == d.Name })
		if i < 0 {
			s.Zones = append(s.Zones, nz)
			res.log(slog.LevelInfo, "zone added: declared", "zone", nz.Name, "mode", nz.Mode, "provider", nz.Provider)
			res.Checks = append(res.Checks, nz)
			continue
		}
		cur := s.Zones[i]
		changed := cur.Mode != nz.Mode || cur.Provider != nz.Provider || cur.Token != nz.Token
		s.Zones[i] = nz
		switch {
		case changed:
			res.log(slog.LevelInfo, "zone edited: declared", "zone", nz.Name, "mode", nz.Mode, "provider", nz.Provider)
			res.Checks = append(res.Checks, nz)
		case !cur.Declared:
			res.log(slog.LevelInfo, "zone adopted: declared", "zone", nz.Name)
		}
	}
	slices.SortFunc(s.Zones, func(a, b Zone) int { return strings.Compare(a.Name, b.Name) })
	return nil
}

// zonesOf is the Zones the lines name, by name only: enough to ask which
// Hostnames they contain.
func zonesOf(want []DeclaredZone) []Zone {
	out := make([]Zone, len(want))
	for i, d := range want {
		out[i] = Zone{Name: d.Name}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (s *State) applyOutposts(want []DeclaredOutpost, res *DeclaredResult) {
	if len(want) == 0 {
		var names []string
		for i := range s.Outposts {
			if s.Outposts[i].Declared {
				s.Outposts[i].Declared = false
				names = append(names, s.Outposts[i].Name)
			}
		}
		if len(names) > 0 {
			res.log(slog.LevelWarn, "no TNL_OUTPOST_* is set, so the declared Outposts stay and the CLI manages them again; remove one with `tnl outpost rm`", "outposts", strings.Join(names, ","))
		}
		return
	}
	wanted := map[string]DeclaredOutpost{}
	for _, d := range want {
		wanted[d.Name] = d
	}

	// An Outpost that isn't declared goes, as `outpost rm --with-routes`.
	for _, o := range slices.Clone(s.Outposts) {
		if _, keep := wanted[o.Name]; keep {
			continue
		}
		_, hosts := s.RemoveOutpost(o.Name)
		res.Removed = append(res.Removed, hosts...)
		res.log(slog.LevelWarn, "outpost removed: it is no longer declared", "outpost", o.Name, "routes_removed", len(hosts))
	}

	for _, d := range want {
		derived := addr.Derive(d.Name)
		i := slices.IndexFunc(s.Outposts, func(o Outpost) bool { return o.Name == d.Name })
		if i < 0 {
			s.Outposts = append(s.Outposts, Outpost{Name: d.Name, PublicKey: d.Key, Address: derived, Declared: true})
			res.log(slog.LevelInfo, "outpost added: declared", "outpost", d.Name, "address", derived.String())
			continue
		}
		o := &s.Outposts[i]
		adopted := !o.Declared
		rekeyed := o.PublicKey != d.Key
		moved := o.Address != derived
		o.Declared = true
		o.PublicKey, o.Address = d.Key, derived
		if rekeyed || o.OneTimeKey != nil {
			// The old key is revoked at once; the Outpost keeps its name, Routes
			// and Declaration.
			o.OneTimeKey = nil
			o.TokenExpires = time.Time{}
			o.Endpoint = netip.AddrPort{}
		}
		switch {
		case adopted:
			res.log(slog.LevelInfo, "outpost adopted: declared", "outpost", d.Name, "address", derived.String(), "rekeyed", rekeyed, "moved", moved)
		case rekeyed:
			res.log(slog.LevelInfo, "outpost rekeyed: its declared key changed", "outpost", d.Name, "address", derived.String())
		}
	}
}
