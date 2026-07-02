package main

import "testing"

func TestResolveVersion(t *testing.T) {
	prev := version
	t.Cleanup(func() { version = prev })

	version = "1.2.3"
	if got := resolveVersion(); got != "1.2.3" {
		t.Errorf("resolveVersion() = %q, want %q", got, "1.2.3")
	}

	// Without an ldflags-injected version, it falls back to build info (or
	// "dev" if that's unavailable too) — either way, never empty.
	version = "dev"
	if got := resolveVersion(); got == "" {
		t.Error("resolveVersion() = \"\", want a non-empty fallback")
	}
}
