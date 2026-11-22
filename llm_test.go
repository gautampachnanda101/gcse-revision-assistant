package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeOllama imitates the parts of the Ollama HTTP API the app uses.
type fakeOllama struct {
	*httptest.Server
	up       atomic.Bool
	mu       sync.Mutex
	models   []string
	pullBody string
	tagsBad  bool
	lastReq  *http.Request
}

func newFakeOllama(t *testing.T, models ...string) *fakeOllama {
	t.Helper()
	f := &fakeOllama{models: models, pullBody: `{"status":"pulling","total":100,"completed":50}` + "\n" + `{"status":"success"}` + "\n"}
	f.up.Store(true)
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.lastReq = r.Clone(context.Background())
		f.mu.Unlock()
		switch r.URL.Path {
		case "/api/version":
			if !f.up.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			_, _ = io.WriteString(w, `{"version":"0.0.0"}`)
		case "/api/tags":
			if f.tagsBad {
				_, _ = io.WriteString(w, "{not json")
				return
			}
			var ms []map[string]string
			f.mu.Lock()
			for _, m := range f.models {
				ms = append(ms, map[string]string{"name": m})
			}
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"models": ms})
		case "/api/pull":
			_, _ = io.WriteString(w, f.pullBody)
		default:
			_, _ = io.WriteString(w, "proxied "+r.URL.Path)
		}
	}))
	t.Cleanup(f.Close)
	t.Setenv("OLLAMA_HOST", f.URL)
	return f
}

func (f *fakeOllama) last() *http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastReq
}

// ramFake answers `sysctl -n hw.memsize` with gb gigabytes and succeeds for everything else.
func ramFake(gb int64) func(string) fakeResult {
	return func(c string) fakeResult {
		if strings.HasPrefix(c, "sysctl") {
			return ok(strconv.FormatInt(gb<<30, 10))
		}
		return ok("")
	}
}

func TestNewAIManagerHostParsing(t *testing.T) {
	for host, want := range map[string]string{
		"":                       "127.0.0.1:11434",
		"0.0.0.0:11434":          "127.0.0.1:11434",
		"0.0.0.0":                "127.0.0.1:11434",
		"localhost:9999":         "localhost:9999",
		"http://10.0.0.2:1234":   "10.0.0.2:1234",
		"http://%zz":             "127.0.0.1:11434",
		"https://ollama.example": "ollama.example",
	} {
		t.Setenv("OLLAMA_HOST", host)
		if got := newAIManager("", true).base.Host; got != want {
			t.Errorf("OLLAMA_HOST=%q → %q, want %q", host, got, want)
		}
	}
	a := newAIManager("m", false)
	if st := a.status(); st.Phase != phaseIdle || st.Proxy != "/llm" || st.Progress != -1 {
		t.Fatalf("initial status = %+v", st)
	}
}

func TestAIManagerStateTransitions(t *testing.T) {
	a := newAIManager("", false)
	a.disable()
	if a.status().Phase != phaseDisabled {
		t.Fatal("disable")
	}
	a.ensure(context.Background()) // disabled → no-op
	if a.status().Phase != phaseDisabled {
		t.Fatal("ensure must not run while disabled")
	}
	a.enable()
	if a.status().Phase != phaseIdle {
		t.Fatal("enable should reset a disabled helper")
	}
	a.set(phaseReady, "x", 1)
	a.enable()
	if a.status().Phase != phaseReady {
		t.Fatal("enable must not reset a non-disabled helper")
	}
	a.set(phasePulling, "quiet progress", 0.5) // progress updates are not logged
	if orDefault("", "d") != "d" || orDefault("v", "d") != "v" || mb(2_500_000) != "2 MB" {
		t.Fatal("helpers")
	}
	a.shutdown() // no child: no-op
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$", "--", "gcse-helper", "0", "", "")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	a.child = cmd
	a.shutdown()
	_ = cmd.Wait()
}

func TestAIProxyStripsPrefixAndBrowserHeaders(t *testing.T) {
	f := newFakeOllama(t)
	a := newAIManager("", false)
	req := httptest.NewRequest(http.MethodPost, "/llm/api/chat", strings.NewReader("{}"))
	req.Header.Set("Origin", "http://gcse-revision.localhost:8765")
	req.Header.Set("Referer", "http://gcse-revision.localhost:8765/")
	res := httptest.NewRecorder()
	a.proxy().ServeHTTP(res, req)
	if res.Body.String() != "proxied /api/chat" {
		t.Fatalf("proxy body = %q", res.Body.String())
	}
	if up := f.last(); up.Header.Get("Origin") != "" || up.Header.Get("Referer") != "" {
		t.Fatal("browser Origin/Referer must not reach Ollama")
	}

	f.Close()
	res = httptest.NewRecorder()
	a.proxy().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/llm/api/tags", nil))
	if res.Code != http.StatusBadGateway || !strings.Contains(res.Body.String(), "not running yet") {
		t.Fatalf("down proxy = %d %q", res.Code, res.Body.String())
	}
}

func TestModelSelection(t *testing.T) {
	for gb, want := range map[int]string{0: "llama3.2", 4: "qwen3:1.7b", 8: "llama3.2", 13: "llama3.2", 14: "gemma3:4b", 64: "gemma3:4b"} {
		if got := modelForRAM(gb); got != want {
			t.Errorf("modelForRAM(%d) = %s, want %s", gb, got, want)
		}
	}
	if baseName("llama3.2:latest") != "llama3.2" || baseName("gemma3:4b") != "gemma3:4b" {
		t.Fatal("baseName")
	}
}

func TestEnsureModel(t *testing.T) {
	isolateHome(t)
	withOS(t, "darwin", "arm64")
	installFakeExec(t, ramFake(16))
	f := newFakeOllama(t, "nomic-embed-text:latest", "llama3.2:latest")

	a := newAIManager("", true)
	if m, err := a.ensureModel(context.Background()); err != nil || m != "llama3.2:latest" {
		t.Fatalf("reuse a good model: %q %v", m, err)
	}
	if a.status().RAMGB != 16 {
		t.Fatalf("RAM = %d", a.status().RAMGB)
	}

	a = newAIManager("llama3.2", true)
	if m, _ := a.ensureModel(context.Background()); m != "llama3.2:latest" {
		t.Fatalf("explicit model already present: %q", m)
	}

	t.Setenv("GCSE_REVISION_MODEL", "phi4-mini")
	a = newAIManager("", true)
	if m, err := a.ensureModel(context.Background()); err != nil || m != "phi4-mini" {
		t.Fatalf("env model pulled: %q %v", m, err)
	}
	t.Setenv("GCSE_REVISION_MODEL", "")

	f.mu.Lock()
	f.models = []string{"nomic-embed-text"}
	f.mu.Unlock()
	a = newAIManager("", true)
	if m, err := a.ensureModel(context.Background()); err != nil || m != "gemma3:4b" {
		t.Fatalf("RAM-sized pull: %q %v", m, err)
	}
	a = newAIManager("", false)
	errContains(t, func() error { _, err := a.ensureModel(context.Background()); return err }(), "--offline")

	f.tagsBad = true
	if _, err := newAIManager("", true).ensureModel(context.Background()); err == nil {
		t.Fatal("bad tags JSON should fail")
	}
	f.Close()
	if _, err := newAIManager("", true).listModels(context.Background()); err == nil {
		t.Fatal("unreachable Ollama should fail")
	}
}

func TestPull(t *testing.T) {
	f := newFakeOllama(t)
	a := newAIManager("", true)
	out := captureStdout(t, func() {
		if err := a.pull(context.Background(), "m"); err != nil {
			t.Fatal(err)
		}
	})
	mustContain(t, out, "Downloading the AI model m… 50%")
	f.pullBody = "garbage\n" + `{"error":"model not found"}` + "\n"
	errContains(t, a.pull(context.Background(), "m"), "model not found")
	f.pullBody = `{"status":"pulling"}` + "\n"
	errContains(t, a.pull(context.Background(), "m"), "ended unexpectedly")
	f.pullBody = strings.Repeat("x", 2<<20)
	if err := a.pull(context.Background(), "m"); err == nil {
		t.Fatal("over-long line should fail")
	}
	f.Close()
	if err := a.pull(context.Background(), "m"); err == nil {
		t.Fatal("unreachable Ollama should fail")
	}
}

func TestEnsureEndToEnd(t *testing.T) {
	isolateHome(t)
	withOS(t, "darwin", "arm64")
	installFakeExec(t, ramFake(8))
	f := newFakeOllama(t, "llama3.2:latest")
	a := newAIManager("", true)
	a.ensure(context.Background())
	if st := a.status(); st.Phase != phaseReady || st.Model != "llama3.2:latest" {
		t.Fatalf("status = %+v", st)
	}
	a.ensure(context.Background()) // already ready and up: returns immediately
	if a.status().Phase != phaseReady {
		t.Fatal("second ensure changed state")
	}

	f.tagsBad = true
	a = newAIManager("", true)
	a.ensure(context.Background())
	if a.status().Phase != phaseError {
		t.Fatalf("model failure should surface as error, got %+v", a.status())
	}

	f.up.Store(false)
	t.Setenv("GCSE_REVISION_PRIVATE_OLLAMA", "1")
	a = newAIManager("", false)
	a.ensure(context.Background())
	if st := a.status(); st.Phase != phaseError || !strings.Contains(st.Message, "--offline") {
		t.Fatalf("offline without Ollama = %+v", st)
	}
}

func TestEnsureServerStartsOllama(t *testing.T) {
	isolateHome(t)
	withOS(t, "linux", "amd64")
	setVar(t, &ollamaPollInterval, time.Millisecond)
	f := newFakeOllama(t)
	f.up.Store(false)
	bin := fakeBinary(t, filepath.Join(privateOllamaDir(), "bin"), "ollama")
	t.Setenv("GCSE_REVISION_PRIVATE_OLLAMA", "1")
	fx := installFakeExec(t, func(c string) fakeResult {
		if strings.HasSuffix(c, "ollama serve") {
			f.up.Store(true)
		}
		return ok("")
	})
	a := newAIManager("", true)
	if err := a.ensureServer(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !fx.called("ollama serve") || a.child == nil {
		t.Fatalf("calls = %v", fx.Calls())
	}
	_ = bin

	// Never comes up.
	f.up.Store(false)
	installFakeExec(t, func(string) fakeResult { return ok("") })
	setVar(t, &ollamaStartPolls, 2)
	errContains(t, newAIManager("", true).ensureServer(context.Background()), "didn't start")

	// Cancelled while waiting.
	ctx, cancel := context.WithCancel(context.Background())
	setVar(t, &ollamaStartPolls, 1000)
	setVar(t, &ollamaPollInterval, time.Hour)
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if err := newAIManager("", true).ensureServer(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}

	// Start failure.
	setVar(t, &execCommand, func(string, ...string) *exec.Cmd { return exec.Command(filepath.Join(t.TempDir(), "nope")) })
	errContains(t, newAIManager("", true).ensureServer(context.Background()), "couldn't start")

	// Not installed, online, install fails (unsupported OS).
	if err := os.RemoveAll(privateOllamaDir()); err != nil {
		t.Fatal(err)
	}
	withOS(t, "plan9", "amd64")
	errContains(t, newAIManager("", true).ensureServer(context.Background()), "couldn't install the AI helper")
}

func TestFindOllama(t *testing.T) {
	home := isolateHome(t)
	setVar(t, &lookPath, func(string) (string, error) { return "", errors.New("not found") })
	withOS(t, "linux", "amd64")
	if got := findOllama(); got != "" && !strings.HasPrefix(got, "/usr") {
		t.Fatalf("unexpected ollama %q", got)
	}
	setVar(t, &lookPath, func(string) (string, error) { return "/path/ollama", nil })
	if findOllama() != "/path/ollama" {
		t.Fatal("PATH lookup should win")
	}
	setVar(t, &lookPath, func(string) (string, error) { return "", errors.New("not found") })

	withOS(t, "darwin", "arm64")
	priv := fakeBinary(t, filepath.Join(privateOllamaDir(), "x"), "ollama")
	if got := findOllama(); got != priv && !strings.Contains(got, "Ollama.app") && !strings.HasSuffix(got, "bin/ollama") {
		t.Fatalf("darwin = %q", got)
	}
	t.Setenv("GCSE_REVISION_PRIVATE_OLLAMA", "1")
	if findOllama() != priv {
		t.Fatal("private mode must only use the private copy")
	}
	t.Setenv("GCSE_REVISION_PRIVATE_OLLAMA", "")

	withOS(t, "windows", "amd64")
	win := filepath.Join(home, "AppData", "Local", `Programs\Ollama\ollama.exe`) // same expression as production, valid on every OS
	writeFile(t, win, "MZ")
	if got := findOllama(); got != win {
		t.Fatalf("windows = %q, want %q", got, win)
	}
	if exeName("ollama") != "ollama.exe" {
		t.Fatal("exeName on windows")
	}
	if findFile(t.TempDir(), "nothing") != "" {
		t.Fatal("findFile should return empty when absent")
	}
}

// ollamaRelease serves sha256sum.txt and one archive under ollamaReleaseURL.
func ollamaRelease(t *testing.T, asset string, archive []byte, sumOverride string) {
	t.Helper()
	sum := sha256.Sum256(archive)
	sums := hex.EncodeToString(sum[:]) + "  ./" + asset + "\n"
	if sumOverride != "" {
		sums = sumOverride
	}
	srv := servePaths(t, map[string]string{"/sha256sum.txt": sums, "/" + asset: string(archive)})
	setVar(t, &ollamaReleaseURL, srv.URL+"/")
}

type tarEntry struct {
	name, link string
	typ        byte
	body       string
}

func makeTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o755, Size: int64(len(e.body)), Linkname: e.link}
		if e.typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			_, _ = tw.Write([]byte(e.body))
		}
	}
	_ = tw.Close()
	return buf.Bytes()
}

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Bytes()
}

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	return buf.Bytes()
}

var ollamaTar = []tarEntry{
	{name: "bin/", typ: tar.TypeDir},
	{name: "bin/ollama", typ: tar.TypeReg, body: "ELF"},
	{name: "lib/link", typ: tar.TypeSymlink, link: "../bin/ollama"},
	{name: "lib/abs", typ: tar.TypeSymlink, link: "/etc/passwd"},
	{name: "lib/ok", typ: tar.TypeSymlink, link: "libfoo.so"},
}

func TestInstallOllamaDarwinTgz(t *testing.T) {
	isolateHome(t)
	withOS(t, "darwin", "arm64")
	ollamaRelease(t, "ollama-darwin.tgz", gz(t, makeTar(t, ollamaTar)), "")
	a := newAIManager("", true)
	var bin string
	captureStdout(t, func() {
		var err error
		if bin, err = a.installOllama(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	if b, _ := os.ReadFile(bin); string(b) != "ELF" {
		t.Fatalf("installed binary = %q", b)
	}
	if _, err := os.Lstat(filepath.Join(privateOllamaDir(), "lib", "abs")); !os.IsNotExist(err) {
		t.Fatal("absolute symlink must be skipped")
	}
	if _, err := os.Lstat(filepath.Join(privateOllamaDir(), "lib", "link")); !os.IsNotExist(err) {
		t.Fatal("parent-escaping symlink must be skipped")
	}

	// Archive without the program.
	ollamaRelease(t, "ollama-darwin.tgz", gz(t, makeTar(t, []tarEntry{{name: "README", typ: tar.TypeReg, body: "hi"}})), "")
	captureStdout(t, func() {
		errContains(t, func() error { _, err := a.installOllama(context.Background()); return err }(), "didn't contain")
	})
	// Not gzip.
	ollamaRelease(t, "ollama-darwin.tgz", []byte("plain"), "")
	captureStdout(t, func() {
		if _, err := a.installOllama(context.Background()); err == nil {
			t.Fatal("non-gzip archive should fail")
		}
	})
	// Traversal inside the archive.
	ollamaRelease(t, "ollama-darwin.tgz", gz(t, makeTar(t, []tarEntry{{name: "../evil", typ: tar.TypeReg, body: "x"}})), "")
	captureStdout(t, func() {
		errContains(t, func() error { _, err := a.installOllama(context.Background()); return err }(), "escapes destination")
	})
	// Checksum mismatch.
	ollamaRelease(t, "ollama-darwin.tgz", gz(t, makeTar(t, ollamaTar)), strings.Repeat("0", 64)+"  ollama-darwin.tgz\n")
	captureStdout(t, func() {
		errContains(t, func() error { _, err := a.installOllama(context.Background()); return err }(), "checksum mismatch")
	})
	// No published checksum.
	ollamaRelease(t, "ollama-darwin.tgz", nil, "abc  other.tgz\n")
	errContains(t, func() error { _, err := a.installOllama(context.Background()); return err }(), "no published checksum")
	// Checksum list unavailable.
	setVar(t, &ollamaReleaseURL, "http://127.0.0.1:1/")
	if _, err := a.installOllama(context.Background()); err == nil {
		t.Fatal("unreachable release should fail")
	}
}

func TestInstallOllamaWindowsZip(t *testing.T) {
	isolateHome(t)
	withOS(t, "windows", "amd64")
	ollamaRelease(t, "ollama-windows-amd64.zip", makeZip(t, map[string]string{"lib/": "", "ollama.exe": "MZ"}), "")
	a := newAIManager("", true)
	captureStdout(t, func() {
		bin, err := a.installOllama(context.Background())
		if err != nil || filepath.Base(bin) != "ollama.exe" {
			t.Fatalf("zip install: %q %v", bin, err)
		}
	})
	ollamaRelease(t, "ollama-windows-amd64.zip", []byte("not a zip"), "")
	captureStdout(t, func() {
		if _, err := a.installOllama(context.Background()); err == nil {
			t.Fatal("corrupt zip should fail")
		}
	})
}

func TestInstallOllamaLinux(t *testing.T) {
	isolateHome(t)
	withOS(t, "linux", "amd64")
	setVar(t, &lookPath, func(name string) (string, error) { return "/usr/bin/" + name, nil })
	ollamaRelease(t, "ollama-linux-amd64.tar.zst", []byte("zstd-compressed"), "")
	installFakeExec(t, func(c string) fakeResult {
		if strings.HasPrefix(c, "zstd -dc") {
			return okBytes(makeTar(t, ollamaTar))
		}
		return ok("")
	})
	a := newAIManager("", true)
	captureStdout(t, func() {
		if _, err := a.installOllama(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	installFakeExec(t, func(string) fakeResult { return fakeResult{stdout: "", code: 1} })
	captureStdout(t, func() {
		if _, err := a.installOllama(context.Background()); err == nil {
			t.Fatal("zstd failure should fail the install")
		}
	})
	installFakeExec(t, func(string) fakeResult {
		return okBytes(makeTar(t, []tarEntry{{name: "../x", typ: tar.TypeReg, body: "x"}}))
	})
	captureStdout(t, func() {
		if _, err := a.installOllama(context.Background()); err == nil {
			t.Fatal("escaping entry should fail")
		}
	})
	setVar(t, &execCommandContext, func(context.Context, string, ...string) *exec.Cmd {
		return exec.Command(filepath.Join(t.TempDir(), "nope"))
	})
	captureStdout(t, func() {
		if _, err := a.installOllama(context.Background()); err == nil {
			t.Fatal("zstd start failure should fail")
		}
	})

	withOS(t, "plan9", "amd64")
	errContains(t, func() error { _, err := a.installOllama(context.Background()); return err }(), "no Ollama build")
}

func TestInstallOllamaScript(t *testing.T) {
	isolateHome(t)
	withOS(t, "linux", "amd64")
	a := newAIManager("", true)
	found := false
	setVar(t, &lookPath, func(name string) (string, error) {
		switch {
		case name == "curl":
			return "/usr/bin/curl", nil
		case name == "ollama" && found:
			return "/usr/local/bin/ollama", nil
		}
		return "", errors.New("not found")
	})
	installFakeExec(t, func(string) fakeResult { found = true; return ok("") })
	if bin, err := a.installOllama(context.Background()); err != nil || bin != "/usr/local/bin/ollama" {
		t.Fatalf("script install (no zstd): %q %v", bin, err)
	}
	found = false
	installFakeExec(t, func(string) fakeResult { return ok("") })
	if _, err := a.installOllamaScript(context.Background()); err == nil {
		// Some hosts genuinely have /usr/local/bin/ollama or /usr/bin/ollama.
		if _, statErr := os.Stat("/usr/local/bin/ollama"); statErr != nil {
			if _, statErr2 := os.Stat("/usr/bin/ollama"); statErr2 != nil {
				t.Fatal("installer that leaves no ollama should fail")
			}
		}
	}
	installFakeExec(t, func(string) fakeResult { return fail("sudo: no tty") })
	if _, err := a.installOllamaScript(context.Background()); err == nil {
		t.Fatal("failed installer should fail")
	}
	setVar(t, &lookPath, func(string) (string, error) { return "", errors.New("not found") })
	errContains(t, func() error { _, err := a.installOllamaScript(context.Background()); return err }(), "install `zstd` or `curl`")
}

func TestDownload(t *testing.T) {
	isolateHome(t)
	body := strings.Repeat("x", 1000)
	sum := sha256.Sum256([]byte(body))
	srv := servePaths(t, map[string]string{"/f": body})
	a := newAIManager("", true)
	dst, err := os.CreateTemp(t.TempDir(), "dl")
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	dl := func(url, sum string) error {
		_, err := a.download(context.Background(), url, dst, sum, "Test")
		return err
	}
	captureStdout(t, func() {
		n, err := a.download(context.Background(), srv.URL+"/f", dst, hex.EncodeToString(sum[:]), "Test")
		if err != nil || n != int64(len(body)) {
			t.Fatalf("download = %d, %v", n, err)
		}
		errContains(t, dl(srv.URL+"/f", "bad"), "checksum mismatch")
		errContains(t, dl(srv.URL+"/missing", "x"), "404")
	})
	if err := dl("http://127.0.0.1:1/x", "x"); err == nil {
		t.Fatal("unreachable download should fail")
	}
	if err := dl("http://bad host/", "x"); err == nil {
		t.Fatal("malformed URL should fail")
	}
	if err := dl(shortBody(t), "x"); err == nil {
		t.Fatal("truncated body should fail")
	}

	pw := &progressWriter{a: a, label: "L"}
	captureStdout(t, func() {
		_, _ = pw.Write(make([]byte, 10))
		pw.report()
	})
	if a.status().Progress != -1 || !strings.Contains(a.status().Message, "L… 0 MB") {
		t.Fatalf("unknown-size progress = %+v", a.status())
	}
}

func TestUnsafeLinkTarget(t *testing.T) {
	for _, l := range []string{"/etc/passwd", `\Windows\System32`, `C:\x`, "C:x", "../up", `a\..\..\b`} {
		if !unsafeLinkTarget(l) {
			t.Errorf("%q must be treated as unsafe", l)
		}
	}
	for _, l := range []string{"libfoo.so", "bin/ollama", "lib/libggml.dylib"} {
		if unsafeLinkTarget(l) {
			t.Errorf("%q is a safe relative target", l)
		}
	}
}

func TestArchiveExtractionSafety(t *testing.T) {
	dest := t.TempDir()
	if _, err := safeJoin(dest, "../../etc/passwd"); err == nil {
		t.Fatal("traversal must be rejected")
	}
	if p, err := safeJoin(dest, "a/b"); err != nil || p != filepath.Join(dest, "a", "b") {
		t.Fatalf("safeJoin = %q %v", p, err)
	}
	if err := untar(bytes.NewReader([]byte("not a tar archive at all, definitely not 512 bytes")), dest); err == nil {
		t.Fatal("corrupt tar should fail")
	}
	z := makeZip(t, map[string]string{"../evil": "x"})
	if err := unzip(bytes.NewReader(z), int64(len(z)), dest); err == nil {
		t.Fatal("zip traversal must be rejected")
	}
	// A file where a directory is needed makes extraction fail cleanly.
	writeFile(t, filepath.Join(dest, "blocker"), "x")
	tb := makeTar(t, []tarEntry{{name: "blocker/sub/", typ: tar.TypeDir}})
	if err := untar(bytes.NewReader(tb), dest); err == nil {
		t.Fatal("mkdir under a file should fail")
	}
	tb = makeTar(t, []tarEntry{{name: "blocker/f", typ: tar.TypeReg, body: "x"}})
	if err := untar(bytes.NewReader(tb), dest); err == nil {
		t.Fatal("write under a file should fail")
	}
	z = makeZip(t, map[string]string{"blocker/d/": ""})
	if err := unzip(bytes.NewReader(z), int64(len(z)), dest); err == nil {
		t.Fatal("zip mkdir under a file should fail")
	}
	z = makeZip(t, map[string]string{"blocker/f": "x"})
	if err := unzip(bytes.NewReader(z), int64(len(z)), dest); err == nil {
		t.Fatal("zip write under a file should fail")
	}
	if err := os.Mkdir(filepath.Join(dest, "isdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeStream(filepath.Join(dest, "isdir"), strings.NewReader("x"), 0o644); err == nil {
		t.Fatal("writing over a directory should fail")
	}
	if err := writeStream(filepath.Join(dest, "f"), errReader{}, 0o644); err == nil {
		t.Fatal("read errors should propagate")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// shortBody serves a response that promises more bytes than it sends, so reading it fails mid-stream.
func shortBody(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("only a little"))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/short"
}
