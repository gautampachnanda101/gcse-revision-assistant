//go:build !windows

package main

import (
	"path/filepath"
	"testing"
)

func TestTotalRAMGB(t *testing.T) {
	withOS(t, "darwin", "arm64")
	installFakeExec(t, func(string) fakeResult { return ok("17179869184\n") })
	if got := totalRAMGB(); got != 16 {
		t.Fatalf("darwin RAM = %d", got)
	}
	installFakeExec(t, func(string) fakeResult { return fail("") })
	if got := totalRAMGB(); got != 0 {
		t.Fatalf("sysctl failure = %d", got)
	}

	withOS(t, "linux", "amd64")
	meminfo := filepath.Join(t.TempDir(), "meminfo")
	writeFile(t, meminfo, "MemFree: 1 kB\nMemTotal:       16303692 kB\n")
	setVar(t, &procMeminfo, meminfo)
	if got := totalRAMGB(); got != 16 {
		t.Fatalf("linux RAM = %d", got)
	}
	writeFile(t, meminfo, "MemFree: 1 kB\n")
	if got := totalRAMGB(); got != 0 {
		t.Fatalf("no MemTotal = %d", got)
	}
	setVar(t, &procMeminfo, filepath.Join(t.TempDir(), "missing"))
	if got := totalRAMGB(); got != 0 {
		t.Fatalf("missing meminfo = %d", got)
	}
}
