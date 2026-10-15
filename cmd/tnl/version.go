package main

import "runtime/debug"

// version is set by the release build with -ldflags "-X main.version=...".
var version = "dev"

// versionInfo returns the version and the build (the VCS revision, marked
// "-dirty" for a modified tree) that the Edge and Outposts log.
func versionInfo() (v, build string) {
	v, build = version, "unknown"
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	dirty := false
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			build = s.Value
			if len(build) > 12 {
				build = build[:12]
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if dirty && build != "unknown" {
		build += "-dirty"
	}
	return
}
