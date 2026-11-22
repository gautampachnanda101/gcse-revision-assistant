package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chdir switches the working directory for one test (t.Chdir needs Go 1.24; go.mod targets 1.22).
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

// captureStderr runs fn and returns what it printed to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	fn()
	w.Close()
	os.Stderr = orig
	return <-done
}

// runCLI runs the command line and returns exit code, stdout and stderr.
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var code int
	var stdout string
	stderr := captureStderr(t, func() {
		stdout = captureStdout(t, func() { code = runCommandLine(args) })
	})
	return code, stdout, stderr
}

func TestRunInformationalCommands(t *testing.T) {
	isolateHome(t)
	for _, tc := range []struct {
		args     []string
		code     int
		inStdout string
		inStderr string
	}{
		{[]string{"version"}, 0, "gcse-revision " + version, ""},
		{[]string{"help"}, 0, "", "Usage"},
		{[]string{"help", "service"}, 0, "Background service", ""},
		{[]string{"help", "nope"}, 1, "", "no help topic"},
		{[]string{"--help"}, 0, "", "Usage"},
		{[]string{"update", "--help"}, 0, "", "Usage"},
		{[]string{"--offline", "version"}, 0, "gcse-revision", ""},
		{[]string{"--offline", "version", "--help"}, 0, "", "Usage"},
		{[]string{"--offline", "version", "--bogus"}, 2, "", "flag provided but not defined"},
		{[]string{"--bogus"}, 2, "", "flag provided but not defined"},
		{[]string{"frobnicate"}, 2, "", "Usage"},
		{[]string{"completion", "zsh"}, 0, "#compdef gcse-revision", ""},
		{[]string{"completion", "cmd"}, 1, "", "completion shell must be"},
		{[]string{"keygen"}, 0, "PUBLIC", ""},
		{[]string{"export"}, 1, "", "usage: gcse-revision export"},
		{[]string{"import"}, 1, "", "usage: gcse-revision import"},
		{[]string{"build"}, 1, "", "usage: gcse-revision build"},
		{[]string{"update", "--repo", ""}, 1, "", "no source repo configured"},
		{[]string{"upgrade"}, 1, "", "development builds"},
	} {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			code, stdout, stderr := runCLI(t, tc.args...)
			if code != tc.code || !strings.Contains(stdout, tc.inStdout) || !strings.Contains(stderr, tc.inStderr) {
				t.Fatalf("run(%q) = %d\nstdout: %s\nstderr: %s", tc.args, code, stdout, stderr)
			}
		})
	}
}

func TestRunServeAndServiceCommands(t *testing.T) {
	isolateHome(t)
	withOS(t, "linux", "amd64")
	installFakeExec(t, func(string) fakeResult { return fakeResult{stdout: "inactive", code: 3} })
	port := fakeApp(t, version)
	if code, _, stderr := runCLI(t, "serve", "--port", itoa(port), "--no-browser", "--offline"); code != 0 {
		t.Fatalf("serve onto a running instance = %d %s", code, stderr)
	}
	if code, stdout, _ := runCLI(t, "service", "status"); code != 0 || !strings.Contains(stdout, "stopped") {
		t.Fatalf("service status = %d %s", code, stdout)
	}
	exe := fakeBinary(t, t.TempDir(), "gcse-revision")
	withExecutable(t, exe, nil)
	if code, stdout, _ := runCLI(t, "doctor", "--port", itoa(port)); code != 0 || !strings.Contains(stdout, "Running version") {
		t.Fatalf("doctor = %d %s", code, stdout)
	}
}

func TestRunSetupAI(t *testing.T) {
	isolateHome(t)
	withOS(t, "darwin", "arm64")
	installFakeExec(t, ramFake(8))
	f := newFakeOllama(t, "llama3.2")
	if code, _, stderr := runCLI(t, "setup-ai"); code != 0 {
		t.Fatalf("setup-ai = %d %s", code, stderr)
	}
	f.tagsBad = true
	if code, _, _ := runCLI(t, "setup-ai", "--offline"); code != 1 {
		t.Fatalf("failing setup-ai = %d", code)
	}
}

func TestRunUpdateFromMirror(t *testing.T) {
	isolateHome(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	setVar(t, &contentPubKey, base64.StdEncoding.EncodeToString(pub))
	mb := renderManifest(manifest{Schema: 1})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me/repo/mirror/subjects.js":
			_, _ = w.Write(mb)
		case "/me/repo/mirror/subjects.js.sig":
			_, _ = io.WriteString(w, base64.StdEncoding.EncodeToString(ed25519.Sign(priv, mb)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("GCSE_REVISION_RAW_BASE", srv.URL)
	if code, stdout, stderr := runCLI(t, "update", "--repo", "me/repo", "--branch", "mirror"); code != 0 || !strings.Contains(stdout, "up to date") {
		t.Fatalf("update = %d %s %s", code, stdout, stderr)
	}
}

func TestRunMaintainerCommands(t *testing.T) {
	isolateHome(t)
	repoRoot, _ := os.Getwd()
	work := t.TempDir()

	// export → import → validate, using a shipped pack.
	bundle := filepath.Join(work, "sikhism.pack.json")
	if code, stdout, stderr := runCLI(t, "export", filepath.Join(repoRoot, "data", "aqa-rs-a-sikhism.js"), bundle); code != 0 || !strings.Contains(stdout, "Wrote") {
		t.Fatalf("export = %d %s %s", code, stdout, stderr)
	}
	chdir(t, work)
	writeFile(t, filepath.Join(work, "data", "s.js"), mustRead(t, filepath.Join(repoRoot, "data", "aqa-rs-a-sikhism.js")))
	if code, _, stderr := runCLI(t, "export", "data/s.js"); code != 0 {
		t.Fatalf("export default name = %d %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(work, "data", "s.pack.json")); err != nil {
		t.Fatal("default export path not used")
	}
	if code, _, stderr := runCLI(t, "import", bundle); code != 0 {
		t.Fatalf("import = %d %s", code, stderr)
	}
	if code, stdout, _ := runCLI(t, "validate", "data/s.js"); code != 0 || !strings.Contains(stdout, "ok") {
		t.Fatalf("validate = %d %s", code, stdout)
	}

	// build a content folder, regenerate the manifest, sign and verify.
	if err := os.Remove(filepath.Join(work, "data", "s.js")); err != nil {
		t.Fatal(err)
	}
	copyTree(t, filepath.Join(repoRoot, "content", "aqa-rs-a-sikhism"), filepath.Join(work, "content", "aqa-rs-a-sikhism"))
	if code, stdout, stderr := runCLI(t, "build", "content/aqa-rs-a-sikhism"); code != 0 || !strings.Contains(stdout, "Built") {
		t.Fatalf("build = %d %s %s", code, stdout, stderr)
	}
	if code, _, stderr := runCLI(t, "build", "content/does-not-exist"); code != 1 {
		t.Fatalf("build of a missing folder = %d %s", code, stderr)
	}
	if code, _, _ := runCLI(t, "manifest"); code != 0 {
		t.Fatal("manifest")
	}
	if code, _, _ := runCLI(t, "manifest", work); code != 0 {
		t.Fatal("manifest <dir>")
	}
	withSigningKey(t)
	if code, _, stderr := runCLI(t, "sign"); code != 0 {
		t.Fatalf("sign = %d %s", code, stderr)
	}
	if code, stdout, _ := runCLI(t, "verify", work); code != 0 || !strings.Contains(stdout, "OK") {
		t.Fatalf("verify = %d %s", code, stdout)
	}
	if code, _, stderr := runCLI(t, "encrypt-private"); code != 1 || !strings.Contains(stderr, "private pack key") {
		t.Fatalf("encrypt-private without a key = %d %s", code, stderr)
	}
}

func TestMainExitsWithRunCode(t *testing.T) {
	var got = -1
	setVar(t, &exitProcess, func(code int) { got = code })
	origArgs := os.Args
	os.Args = []string{"gcse-revision", "version"}
	defer func() { os.Args = origArgs }()
	captureStdout(t, main)
	if got != 0 {
		t.Fatalf("exit code = %d", got)
	}
	var b bytes.Buffer
	usageTo := captureStderr(t, usage)
	b.WriteString(usageTo)
	mustContain(t, b.String(), "Usage", "Flags", "-port")
	_ = context.Background
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
