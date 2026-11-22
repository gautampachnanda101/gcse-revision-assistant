package main

// Shared unit-test harness.
//
// Tests never run real system commands (launchctl, systemctl, schtasks, brew, scoop, ollama, powershell …).
// fakeExec swaps the execCommand/execCommandContext seams for a stub that re-runs this test binary as a
// helper process (TestHelperProcess), which prints scripted stdout/stderr and exits with a scripted code.
// That works identically on Linux, macOS and Windows runners.
//
// isolateHome points every per-user directory (home, cache, config, private packs) at a temporary folder so
// tests can't touch the developer's real LaunchAgents, systemd units, caches or packs.

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeResult is what a faked command prints and how it exits.
type fakeResult struct {
	stdout, stderr string
	code           int
}

func ok(stdout string) fakeResult   { return fakeResult{stdout: stdout} }
func fail(output string) fakeResult { return fakeResult{stdout: output, code: 1} }
func failCode(code int) fakeResult  { return fakeResult{code: code} }
func okBytes(b []byte) fakeResult   { return fakeResult{stdout: string(b)} }
func joinCmd(name string, args []string) string {
	return strings.TrimSpace(filepath.Base(name) + " " + strings.Join(args, " "))
}

type fakeExec struct {
	mu      sync.Mutex
	calls   []string
	handler func(cmdline string) fakeResult
}

// installFakeExec routes every command through handler. The handler receives "<basename> <args…>".
func installFakeExec(t *testing.T, handler func(cmdline string) fakeResult) *fakeExec {
	t.Helper()
	f := &fakeExec{handler: handler}
	origCmd, origCtx := execCommand, execCommandContext
	execCommand = func(name string, args ...string) *exec.Cmd { return f.command(context.Background(), name, args) }
	execCommandContext = func(ctx context.Context, name string, args ...string) *exec.Cmd { return f.command(ctx, name, args) }
	t.Cleanup(func() { execCommand, execCommandContext = origCmd, origCtx })
	return f
}

func (f *fakeExec) command(ctx context.Context, name string, args []string) *exec.Cmd {
	line := joinCmd(name, args)
	f.mu.Lock()
	f.calls = append(f.calls, line)
	f.mu.Unlock()
	r := f.handler(line)
	enc := base64.StdEncoding.EncodeToString
	// The script travels in argv (not env) because some callers replace cmd.Env.
	return exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperProcess$", "--", "gcse-helper",
		strconv.Itoa(r.code), enc([]byte(r.stdout)), enc([]byte(r.stderr)))
}

func (f *fakeExec) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeExec) called(prefix string) bool {
	for _, c := range f.Calls() {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// TestHelperProcess is not a real test: it is the body of every faked command.
func TestHelperProcess(t *testing.T) {
	args := os.Args
	for i, a := range args {
		if a == "--" && i+1 < len(args) && args[i+1] == "gcse-helper" {
			code, _ := strconv.Atoi(args[i+2])
			out, _ := base64.StdEncoding.DecodeString(args[i+3])
			errOut, _ := base64.StdEncoding.DecodeString(args[i+4])
			_, _ = os.Stdout.Write(out)
			_, _ = os.Stderr.Write(errOut)
			os.Exit(code)
		}
	}
}

// isolateHome redirects all per-user locations to a fresh temp dir and returns it.
func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for k, v := range map[string]string{
		"HOME":                         home,
		"USERPROFILE":                  home,
		"XDG_CACHE_HOME":               filepath.Join(home, ".cache"),
		"XDG_CONFIG_HOME":              filepath.Join(home, ".config"),
		"LOCALAPPDATA":                 filepath.Join(home, "AppData", "Local"),
		"APPDATA":                      filepath.Join(home, "AppData", "Roaming"),
		"GCSE_REVISION_PRIVATE_PACKS":  filepath.Join(home, "private-packs"),
		"GCSE_PRIVATE_KEY":             "",
		"GCSE_REVISION_AUTO_UPDATE":    "",
		"GCSE_REVISION_MODEL":          "",
		"GCSE_REVISION_PRIVATE_OLLAMA": "",
		"NO_COLOR":                     "1",
	} {
		t.Setenv(k, v)
	}
	orig := currentUser
	currentUser = func() (*user.User, error) {
		return &user.User{Uid: "501", Username: "tester", HomeDir: home}, nil
	}
	t.Cleanup(func() { currentUser = orig })
	return home
}

// withOS pretends to run on goos/goarch for the duration of the test.
func withOS(t *testing.T, goos, goarch string) {
	t.Helper()
	origOS, origArch := hostOS, hostArch
	hostOS, hostArch = goos, goarch
	t.Cleanup(func() { hostOS, hostArch = origOS, origArch })
}

// withExecutable makes executable() report path (or fail with err).
func withExecutable(t *testing.T, path string, err error) {
	t.Helper()
	orig := executable
	executable = func() (string, error) { return path, err }
	t.Cleanup(func() { executable = orig })
}

// noSleep makes the sleep seam return immediately.
func noSleep(t *testing.T) {
	t.Helper()
	orig := sleep
	sleep = func(time.Duration) {}
	t.Cleanup(func() { sleep = orig })
}

// setVar sets *p to v for the duration of the test.
func setVar[T any](t *testing.T, p *T, v T) {
	t.Helper()
	orig := *p
	*p = v
	t.Cleanup(func() { *p = orig })
}

// fakeApp serves /api/info like a running gcse-revision on 127.0.0.1 and returns its port.
func fakeApp(t *testing.T, appVersion string) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"app":%q,"version":%q}`, appName, appVersion)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	_, p, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(p)
	return port
}

// unusedPort returns a port nothing is listening on.
func unusedPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// captureStdout runs fn and returns what it printed to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	defer func() { os.Stdout = orig }()
	fn()
	w.Close()
	os.Stdout = orig
	return <-done
}

// servePath returns an httptest server answering each path with the given body (404 otherwise).
func servePaths(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, found := routes[r.URL.Path]
		if !found {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// writeFile creates path (and parents) with contents.
func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mustContain fails unless s contains every want.
func mustContain(t *testing.T, s string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(s, w) {
			t.Fatalf("output is missing %q:\n%s", w, s)
		}
	}
}

// errContains fails unless err is non-nil and mentions want.
func errContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error mentioning %q, got nil", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not mention %q", err, want)
	}
}

// appOnPort starts a fake gcse-revision /api/info responder on a specific port (used when a faked
// "start service" command should make the app appear).
func appOnPort(t *testing.T, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("listen on %d: %v", port, err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `{"app":%q,"version":%q}`, appName, version)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
}

// fastService shrinks the service stop/health deadlines and disables sleeps.
func fastService(t *testing.T) {
	t.Helper()
	noSleep(t)
	setVar(t, &serviceStopTimeout, 50*time.Millisecond)
	setVar(t, &serviceHealthTimeout, 50*time.Millisecond)
}

// fakeBinary creates an empty executable file and returns its path.
func fakeBinary(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	writeFile(t, p, "#!/bin/sh\n")
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func itoa(n int) string { return strconv.Itoa(n) }

// blockCacheDir points the per-user cache folder (and so dataDir) underneath a regular file on every OS,
// so anything that writes there fails.
func blockCacheDir(t *testing.T) {
	t.Helper()
	blocked := filepath.Join(t.TempDir(), "file")
	writeFile(t, blocked, "x")
	home := filepath.Join(blocked, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData"))
}

// blockTempDir points os.TempDir at a missing folder on every OS.
func blockTempDir(t *testing.T) {
	t.Helper()
	missing := filepath.Join(t.TempDir(), "missing")
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, missing)
	}
}

// isHelperProcess reports whether this test binary was started by fakeExec as a stand-in command.
func isHelperProcess() bool {
	for i, a := range os.Args {
		if a == "--" && i+1 < len(os.Args) && os.Args[i+1] == "gcse-helper" {
			return true
		}
	}
	return false
}
