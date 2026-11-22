package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAppleBridge(t *testing.T) {
	isolateHome(t)
	withOS(t, "linux", "amd64")
	if appleBridgePath() != "" {
		t.Fatal("no bridge off Apple Silicon")
	}
	if ok, detail := appleAIAvailable(); ok || !strings.Contains(detail, "Apple Silicon") {
		t.Fatalf("linux availability = %v %q", ok, detail)
	}
	if _, err := chatWithAppleAI(nil); err == nil {
		t.Fatal("chat off Apple Silicon should fail")
	}

	withOS(t, "darwin", "arm64")
	if len(appleFMBridge) == 0 {
		t.Skip("bridge binary not embedded in this checkout")
	}
	path, err := ensureAppleBridge()
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); (runtime.GOOS != "windows" && st.Mode()&0o111 == 0) || st.Size() != int64(len(appleFMBridge)) {
		t.Fatal("bridge not installed as an executable copy")
	}
	if again, err := ensureAppleBridge(); err != nil || again != path {
		t.Fatal("an intact bridge should be reused")
	}

	installFakeExec(t, func(c string) fakeResult { return ok("") })
	if ok, detail := appleAIAvailable(); !ok || detail != "Apple Intelligence" {
		t.Fatalf("available = %v %q", ok, detail)
	}
	installFakeExec(t, func(string) fakeResult { return fail("  Apple Intelligence is not enabled  ") })
	if ok, detail := appleAIAvailable(); ok || detail != "Apple Intelligence is not enabled" {
		t.Fatalf("unavailable = %v %q", ok, detail)
	}

	installFakeExec(t, func(string) fakeResult { return ok(`{"model":"m","content":"Good answer"}`) })
	if got, err := chatWithAppleAI([]chatMessage{{Role: "user", Content: "hi"}}); err != nil || got != "Good answer" {
		t.Fatalf("chat = %q %v", got, err)
	}
	installFakeExec(t, func(string) fakeResult { return ok("not json") })
	errContains(t, func() error { _, err := chatWithAppleAI(nil); return err }(), "invalid Apple Intelligence response")
	installFakeExec(t, func(string) fakeResult { return fakeResult{stderr: `{"error":"model busy"}`, code: 1} })
	errContains(t, func() error { _, err := chatWithAppleAI(nil); return err }(), "model busy")
	installFakeExec(t, func(string) fakeResult { return failCode(2) })
	errContains(t, func() error { _, err := chatWithAppleAI(nil); return err }(), "Apple Intelligence bridge")

	// A stale (wrong size) copy is replaced; an unwritable location fails.
	if err := os.WriteFile(path, []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureAppleBridge(); err != nil {
		t.Fatal(err)
	}
	native := filepath.Dir(path)
	if err := os.RemoveAll(native); err != nil {
		t.Fatal(err)
	}
	writeFile(t, native, "file in the way")
	if _, err := ensureAppleBridge(); err == nil {
		t.Fatal("expected a write failure")
	}
	if _, err := chatWithAppleAI(nil); err == nil {
		t.Fatal("chat should surface the install failure")
	}
}

func TestRunDoctor(t *testing.T) {
	isolateHome(t)
	withOS(t, "linux", "amd64")
	installFakeExec(t, func(string) fakeResult { return ok("active") })
	withExecutable(t, "/opt/homebrew/Cellar/gcse-revision/1.0/bin/gcse-revision", nil)
	setVar(t, &version, "v1.0.0")
	out := captureStdout(t, func() {
		if err := runDoctor(options{port: fakeApp(t, "v0.9.0")}); err != nil {
			t.Fatal(err)
		}
	})
	mustContain(t, out, "package-managed", "running", "healthy", "v0.9.0 (CLI is v1.0.0; run gcse-revision service restart)", "Auto-upgrade", "enabled", "unavailable")

	withOS(t, "darwin", "arm64")
	standaloneExe(t)
	installFakeExec(t, func(c string) fakeResult {
		if strings.Contains(c, "launchctl") {
			return fail("Could not find service")
		}
		return ok("")
	})
	out = captureStdout(t, func() { _ = runDoctor(options{port: fakeApp(t, "v1.0.0"), noAutoUp: true}) })
	mustContain(t, out, "standalone", "stopped", "matches this CLI", "disabled", "Built-in AI", "available (Apple Intelligence)")

	installFakeExec(t, func(string) fakeResult { return failCode(1) })
	setVar(t, &version, "dev")
	out = captureStdout(t, func() { _ = runDoctor(options{port: unusedPort(t)}) })
	mustContain(t, out, "not running", "not supported on this platform", "disabled")

	withExecutable(t, "", errors.New("no exe"))
	if err := runDoctor(options{}); err == nil {
		t.Fatal("executable failure should propagate")
	}
}

func TestANSIStyle(t *testing.T) {
	s := ansiStyle{enabled: true}
	if s.bold("x") != "\x1b[1mx\x1b[0m" || s.dim("x") == "x" {
		t.Fatal("enabled style must emit ANSI codes")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm")
	f, err := os.CreateTemp(t.TempDir(), "notatty")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if newANSIStyle(f).enabled {
		t.Fatal("a regular file is not a terminal")
	}
	if newANSIStyle(&bytes.Buffer{}).enabled {
		t.Fatal("non-file writers never get colour")
	}
	for _, topic := range []string{"update", "upgrade", "completion"} {
		var b bytes.Buffer
		if err := helpTopic(&b, topic); err != nil || b.Len() == 0 {
			t.Fatalf("help %s: %v", topic, err)
		}
	}
	var b bytes.Buffer
	if err := writeCompletion(&b, "pwsh"); err != nil || !strings.Contains(b.String(), "Register-ArgumentCompleter") {
		t.Fatal("pwsh completion")
	}
	_ = exec.ErrNotFound
}
