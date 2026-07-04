package app

import "testing"

func TestDefaultInfo(t *testing.T) {
	info := DefaultInfo()

	if info.ProductName != ProductName {
		t.Fatalf("expected product name %q, got %q", ProductName, info.ProductName)
	}
	if info.CLIName != CLIName {
		t.Fatalf("expected CLI name %q, got %q", CLIName, info.CLIName)
	}
	if info.Version != DefaultVersion {
		t.Fatalf("expected version %q, got %q", DefaultVersion, info.Version)
	}
	if info.Commit != UnknownBuildValue {
		t.Fatalf("expected commit %q, got %q", UnknownBuildValue, info.Commit)
	}
	if info.BuildDate != UnknownBuildValue {
		t.Fatalf("expected build date %q, got %q", UnknownBuildValue, info.BuildDate)
	}
}

func TestNewInfoUsesDefaultsForEmptyBuildValues(t *testing.T) {
	info := NewInfo("", "", "")

	if info.Version != DefaultVersion {
		t.Fatalf("expected empty version to default to %q, got %q", DefaultVersion, info.Version)
	}
	if info.Commit != UnknownBuildValue {
		t.Fatalf("expected empty commit to default to %q, got %q", UnknownBuildValue, info.Commit)
	}
	if info.BuildDate != UnknownBuildValue {
		t.Fatalf("expected empty build date to default to %q, got %q", UnknownBuildValue, info.BuildDate)
	}
}
