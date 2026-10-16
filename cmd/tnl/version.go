package main

import "runtime/debug"

// version is set by the release build with -ldflags "-X main.version=...".
var version = "dev"

// versionInfo returns the version and the VCS revision (marked "-dirty" for a
// modified tree) that the Edge and Outposts log. The build is buildName.
func versionInfo() (v, revision string) {
	v, revision = version, "unknown"
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	dirty := false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
			if len(revision) > 12 {
				revision = revision[:12]
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if dirty && revision != "unknown" {
		revision += "-dirty"
	}
	return
}
