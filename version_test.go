package main

import (
	"runtime/debug"
	"testing"
)

func TestResolvedVersionNonEmpty(t *testing.T) {
	if resolvedVersion() == "" {
		t.Fatal("resolvedVersion() must not be empty")
	}
}

func TestResolvedVersionUsesBuildInfoWhenDefault(t *testing.T) {
	// Default link-time version is "dev"; without -ldflags, we fall back to BuildInfo.
	if version != "dev" {
		t.Skip("main.version was set via -ldflags; skipping BuildInfo assertion")
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		t.Fatal("ReadBuildInfo failed")
	}
	got := resolvedVersion()
	switch info.Main.Version {
	case "", "(devel)":
		if got != "dev" {
			t.Fatalf("Main.Version=%q: got %q, want dev", info.Main.Version, got)
		}
	default:
		if got != info.Main.Version {
			t.Fatalf("Main.Version=%q: got %q", info.Main.Version, got)
		}
	}
}
