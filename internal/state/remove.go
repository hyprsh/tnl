package state

import "slices"

// The removal rules live here, for the commands (`zone rm`, `outpost rm`) and
// for the Edge's declared lines that are gone, which remove the same way.

// ZoneRemoval is what removing a Zone did to the Routes.
type ZoneRemoval struct {
	// Removed are the Hostnames of the Routes made with the CLI that only the
	// Zone contained: they are gone.
	Removed []string
	// Waiting are the Hostnames of declared Routes that only the Zone contained:
	// they are gone as Routes, and their lines wait again ("outside every Zone").
	Waiting []string
	// Moved are the Hostnames of the Routes a parent Zone still contains: they
	// stay, and belong to it from then on.
	Moved []string
}

// RemoveZone removes the named Zone, which must exist, and the Routes that
// would be left outside every Zone (RoutesStrandedBy). The caller has refused
// the removal if it must not strand Routes.
func (s *State) RemoveZone(name string) ZoneRemoval {
	var res ZoneRemoval
	stranded := s.RoutesStrandedBy(name)
	for _, r := range s.Routes {
		switch z, in := s.ZoneOf(r.Hostname); {
		case slices.Contains(stranded, r.Hostname) && s.Declares(r.Outpost):
			res.Waiting = append(res.Waiting, r.Hostname)
		case slices.Contains(stranded, r.Hostname):
			res.Removed = append(res.Removed, r.Hostname)
		case in && z.Name == name:
			res.Moved = append(res.Moved, r.Hostname)
		}
	}
	s.RemoveRoutes(func(r Route) bool { return slices.Contains(stranded, r.Hostname) })
	s.Zones = slices.DeleteFunc(slices.Clone(s.Zones), func(z Zone) bool { return z.Name == name })
	return res
}

// RemoveOutpost removes the named Outpost, which must exist, with its Declaration
// and the Routes through it, and returns it and their Hostnames. The caller has
// refused the removal if Routes must not go with it.
func (s *State) RemoveOutpost(name string) (gone Outpost, hosts []string) {
	i := slices.IndexFunc(s.Outposts, func(o Outpost) bool { return o.Name == name })
	gone = s.Outposts[i]
	hosts = s.RemoveRoutesVia(name)
	s.RemoveDeclaration(name)
	s.Outposts = slices.Delete(slices.Clone(s.Outposts), i, i+1)
	return gone, hosts
}
