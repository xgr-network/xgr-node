package framework

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBinary_PrefersEnvVars(t *testing.T) {
	t.Setenv("EDGE_BINARY", "/tmp/custom-edge")
	t.Setenv("XGRCHAIN_BINARY", "/tmp/custom-xgr")

	got := resolveBinary()
	if got != "/tmp/custom-edge" {
		t.Fatalf("expected EDGE_BINARY to win, got %q", got)
	}
}

func TestResolveBinary_UsesXGRCHAINBinaryEnv(t *testing.T) {
	t.Setenv("EDGE_BINARY", "")
	t.Setenv("XGRCHAIN_BINARY", "/tmp/custom-xgr")

	got := resolveBinary()
	if got != "/tmp/custom-xgr" {
		t.Fatalf("expected XGRCHAIN_BINARY to be used, got %q", got)
	}
}

func TestResolveBinary_FindsXGRChainInPath(t *testing.T) {
	t.Setenv("EDGE_BINARY", "")
	t.Setenv("XGRCHAIN_BINARY", "")

	dir := t.TempDir()
	binPath := filepath.Join(dir, "xgrchain")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("failed to create fake binary: %v", err)
	}

	t.Setenv("PATH", dir)

	got := resolveBinary()
	if got != "xgrchain" {
		t.Fatalf("expected xgrchain from PATH, got %q", got)
	}
}

func TestResolveBinary_DefaultsToXGRChain(t *testing.T) {
	t.Setenv("EDGE_BINARY", "")
	t.Setenv("XGRCHAIN_BINARY", "")

	emptyPathDir := t.TempDir()
	t.Setenv("PATH", emptyPathDir)

	got := resolveBinary()
	if got != "xgrchain" {
		t.Fatalf("expected default xgrchain, got %q", got)
	}
}
