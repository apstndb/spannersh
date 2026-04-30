package main

import "runtime/debug"

// version is the default release identifier. Override at link time, for example:
//
//	go build -ldflags "-X main.version=v1.2.3"
//
// [resolvedVersion] prefers this value when it is not the default "dev". Otherwise it uses
// [debug.BuildInfo].Main.Version (set for `go install module@version` to the tag or pseudo-version).
var version = "dev"

// resolvedVersion returns the string shown by --version.
// 1) -ldflags "-X main.version=..." when not the default "dev".
// 2) BuildInfo Main.Version when non-empty and not "(devel)" (e.g. go install from a tagged module).
// 3) "dev".
func resolvedVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}
