//go:build e2e

// End-to-end tests: build the real binary and drive it as a user would, on Linux, macOS and Windows.
//
//	go test -tags e2e -v -timeout 40m ./...
//
// Environment:
//
//	E2E_REQUIRE_BROWSER=1  fail (rather than skip) if headless Chrome isn't available
//	E2E_AI=1               also install Ollama privately, pull E2E_MODEL (default smollm2:135m) and chat through /llm
package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

var (
	e2eBin  string // binary built with the throwaway public key
	e2ePriv ed25519.PrivateKey
	repoDir string
)

func TestMain(m *testing.M) {
	if isHelperProcess() { // a faked command from the unit-test harness: don't build the e2e binary
		os.Exit(m.Run())
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	e2ePriv = priv
	repoDir, _ = os.Getwd()
	tmp, _ := os.MkdirTemp("", "gcse-e2e-bin")
	e2eBin = filepath.Join(tmp, exeName("gcse-revision"))
	cmd := exec.Command("go", "build", "-o", e2eBin, "-ldflags",
		"-X main.version=e2e -X main.contentPubKey="+base64.StdEncoding.EncodeToString(pub), ".")
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		panic("build failed: " + err.Error())
	}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// userEnv gives the binary its own empty home/cache/config so tests never touch the real machine's state.
func userEnv(t *testing.T, extra ...string) []string {
	home := t.TempDir()
	env := []string{}
	for _, kv := range os.Environ() {
		k := strings.ToUpper(strings.SplitN(kv, "=", 2)[0])
		switch k {
		case "HOME", "USERPROFILE", "XDG_CACHE_HOME", "XDG_CONFIG_HOME", "LOCALAPPDATA", "APPDATA", "OLLAMA_HOST",
			"GCSE_REVISION_REPO", "GCSE_REVISION_BRANCH", "GCSE_REVISION_RAW_BASE", "GCSE_REVISION_PRIVATE_PACKS":
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "HOME="+home, "USERPROFILE="+home,
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"), "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"), "APPDATA="+filepath.Join(home, "AppData", "Roaming"))
	return append(env, extra...)
}

func cacheDirFor(env []string) string {
	get := func(k string) string {
		for _, kv := range env {
			if strings.HasPrefix(kv, k+"=") {
				return strings.TrimPrefix(kv, k+"=")
			}
		}
		return ""
	}
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(get("LOCALAPPDATA"), appName)
	case "darwin":
		return filepath.Join(get("HOME"), "Library", "Caches", appName)
	default:
		return filepath.Join(get("XDG_CACHE_HOME"), appName)
	}
}

func run(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(e2eBin, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	t.Logf("$ gcse-revision %s\n%s", strings.Join(args, " "), out)
	return string(out), err
}

// fakeRemote publishes the repo's packs plus one extra pack, signed with the throwaway key, laid out like
// raw.githubusercontent.com/<owner>/<repo>/<branch>/…
type fakeRemote struct {
	root, dir string
	srv       *httptest.Server
}

func newFakeRemote(t *testing.T) *fakeRemote {
	root := t.TempDir()
	dir := filepath.Join(root, "me", "repo", "content")
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	packs, _ := filepath.Glob(filepath.Join(repoDir, "data", "*.js"))
	if len(packs) == 0 {
		t.Fatal("no packs in data/")
	}
	for _, p := range packs {
		b, _ := os.ReadFile(p)
		os.WriteFile(filepath.Join(dir, "data", filepath.Base(p)), b, 0o644)
	}
	extra, _ := os.ReadFile(packs[0])
	extra = regexp.MustCompile(`"storageId"\s*:\s*"[^"]+"`).ReplaceAll(extra, []byte(`"storageId":"e2e_remote_only"`))
	extra = regexp.MustCompile(`"brandTitle"\s*:\s*"[^"]+"`).ReplaceAll(extra, []byte(`"brandTitle":"E2E Remote Subject"`))
	os.WriteFile(filepath.Join(dir, "data", "e2e-remote-only.js"), extra, 0o644)
	if err := writeManifest(dir); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRemote{root: root, dir: dir}
	fr.sign(t)
	fr.srv = httptest.NewServer(http.FileServer(http.Dir(root)))
	t.Cleanup(fr.srv.Close)
	return fr
}

func (f *fakeRemote) sign(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(f.dir, "subjects.js"))
	if err != nil {
		t.Fatal(err)
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(e2ePriv, b))
	os.WriteFile(filepath.Join(f.dir, sigFile), []byte(sig), 0o644)
}

func (f *fakeRemote) env(t *testing.T, extra ...string) []string {
	return userEnv(t, append([]string{"GCSE_REVISION_RAW_BASE=" + f.srv.URL, "GCSE_REVISION_REPO=me/repo"}, extra...)...)
}

func TestSyncDownloadsSignedContent(t *testing.T) {
	fr := newFakeRemote(t)
	env := fr.env(t)
	if _, err := run(t, env, "update"); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDirFor(env), "data", "e2e-remote-only.js")); err != nil {
		t.Fatalf("remote-only pack not cached: %v", err)
	}
	out, err := run(t, env, "update")
	if err != nil || !strings.Contains(out, "0 downloaded") {
		t.Fatalf("second update should download nothing: %v", err)
	}
}

func TestSyncRejectsBadSignature(t *testing.T) {
	fr := newFakeRemote(t)
	// Content changed after signing (e.g. someone without the key edits the branch).
	m, _ := os.ReadFile(filepath.Join(fr.dir, "subjects.js"))
	os.WriteFile(filepath.Join(fr.dir, "subjects.js"), bytes.Replace(m, []byte("E2E Remote Subject"), []byte("Evil Subject"), 1), 0o644)
	env := fr.env(t)
	out, err := run(t, env, "update")
	if err == nil || !strings.Contains(out, "signature") {
		t.Fatalf("expected signature rejection, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDirFor(env), "data", "e2e-remote-only.js")); err == nil {
		t.Fatal("content was cached despite a bad signature")
	}
}

func TestSyncRejectsTamperedPack(t *testing.T) {
	fr := newFakeRemote(t)
	p := filepath.Join(fr.dir, "data", "e2e-remote-only.js")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, append(b, []byte("\nalert('tampered');\n")...), 0o644) // manifest+signature still valid
	env := fr.env(t)
	out, err := run(t, env, "update")
	if err == nil || !strings.Contains(out, "checksum") {
		t.Fatalf("expected checksum rejection, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDirFor(env), "data", "e2e-remote-only.js")); err == nil {
		t.Fatal("tampered pack was cached")
	}
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

type server struct {
	url string
	cmd *exec.Cmd
	out *bytes.Buffer
}

func startServer(t *testing.T, env []string, args ...string) *server {
	t.Helper()
	port := freePort(t)
	s := &server{url: fmt.Sprintf("http://127.0.0.1:%d", port), out: &bytes.Buffer{}}
	s.cmd = exec.Command(e2eBin, append([]string{"--no-browser", "--port", fmt.Sprint(port)}, args...)...)
	s.cmd.Env = env
	s.cmd.Stdout, s.cmd.Stderr = s.out, s.out
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if runtime.GOOS == "windows" {
			// Kill() leaves the private Ollama child running, which keeps its DLLs locked and breaks TempDir cleanup.
			_ = exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprint(s.cmd.Process.Pid)).Run()
		} else {
			_ = s.cmd.Process.Signal(os.Interrupt)
		}
		done := make(chan struct{})
		go func() { s.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = s.cmd.Process.Kill()
		}
		t.Logf("server log:\n%s", s.out.String())
	})
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if r, err := http.Get(s.url + "/api/info"); err == nil {
			r.Body.Close()
			return s
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("server didn't start:\n%s", s.out.String())
	return nil
}

func get(t *testing.T, url string, hdr ...string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i] == "Host" {
			req.Host = hdr[i+1]
		} else {
			req.Header.Set(hdr[i], hdr[i+1])
		}
	}
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(r.Body)
	return r.StatusCode, string(b)
}

func TestServeOfflineWithSyncedAndPrivatePacks(t *testing.T) {
	fr := newFakeRemote(t)
	privDir := t.TempDir()
	priv, _ := os.ReadFile(filepath.Join(fr.dir, "data", "e2e-remote-only.js"))
	priv = bytes.Replace(priv, []byte("e2e_remote_only"), []byte("e2e_private"), 1)
	os.WriteFile(filepath.Join(privDir, "mine.js"), priv, 0o644)
	env := fr.env(t, "GCSE_REVISION_PRIVATE_PACKS="+privDir)
	if _, err := run(t, env, "update"); err != nil {
		t.Fatal(err)
	}
	fr.srv.Close() // from here on there is no network source at all
	s := startServer(t, env, "--offline", "--no-ai")

	code, body := get(t, s.url+"/subjects.js")
	if code != 200 {
		t.Fatalf("subjects.js: %d", code)
	}
	m, err := parseManifest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, e := range m.Subjects {
		ids[e.ID] = e.File
	}
	for _, want := range []string{"e2e_remote_only", "e2e_private"} {
		if ids[want] == "" {
			t.Errorf("subject %s missing from offline manifest: %v", want, ids)
		}
	}
	for id, f := range ids {
		if c, _ := get(t, s.url+"/"+f); c != 200 {
			t.Errorf("%s (%s): HTTP %d", id, f, c)
		}
	}
	for _, c := range []struct {
		name, path string
		hdr        []string
		want       int
	}{
		{"app", "/", nil, 200},
		{"path traversal", "/data/../main.go", nil, 404},
		{"foreign host (DNS rebinding)", "/api/info", []string{"Host", "evil.example"}, 403},
	} {
		if code, _ := get(t, s.url+c.path, c.hdr...); code != c.want {
			t.Errorf("%s: got %d, want %d", c.name, code, c.want)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, s.url+"/api/llm/setup", nil)
	req.Header.Set("Origin", "http://evil.example")
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != 403 {
		t.Errorf("cross-origin POST should be 403, got %v %v", r, err)
	}
	if code, body := get(t, s.url+"/api/llm/status"); code != 200 || !strings.Contains(body, `"disabled"`) {
		t.Errorf("--no-ai status: %d %s", code, body)
	}

	dom := renderPage(t, s.url+"/")
	if dom == "" {
		return
	}
	for _, re := range []string{`id="total-pill">\d+ questions`, `id="breadcrumb">[^<]+/`} {
		if !regexp.MustCompile(re).MatchString(dom) {
			t.Errorf("rendered page doesn't match %s", re)
		}
	}
	if !strings.Contains(dom, "E2E Remote Subject") {
		t.Error("subject picker doesn't list the downloaded subject")
	}
}

func chromePath() string {
	if p := os.Getenv("CHROME_PATH"); p != "" {
		return p
	}
	for _, n := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	for _, p := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// renderPage loads the page in headless Chrome and returns the DOM after scripts ran ("" = skipped).
func renderPage(t *testing.T, url string) string {
	t.Helper()
	chrome := chromePath()
	if chrome == "" {
		if os.Getenv("E2E_REQUIRE_BROWSER") == "1" {
			t.Fatal("headless Chrome not found")
		}
		t.Log("Chrome not found; skipping browser render check")
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome, "--headless=new", "--disable-gpu", "--no-sandbox", "--no-first-run",
		"--user-data-dir="+t.TempDir(), "--virtual-time-budget=8000", "--dump-dom", url)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("chrome: %v", err)
	}
	return string(out)
}

func TestAIHelperEndToEnd(t *testing.T) {
	if os.Getenv("E2E_AI") != "1" {
		t.Skip("set E2E_AI=1 to run (downloads Ollama + a small model)")
	}
	model := envOr("E2E_MODEL", "smollm2:135m")
	port := freePort(t)
	env := userEnv(t, "GCSE_REVISION_PRIVATE_OLLAMA=1", fmt.Sprintf("OLLAMA_HOST=127.0.0.1:%d", port),
		"OLLAMA_MODELS="+filepath.Join(t.TempDir(), "models"))
	s := startServer(t, env, "--model", model, "--repo=")

	deadline := time.Now().Add(30 * time.Minute)
	var st aiStatus
	for time.Now().Before(deadline) {
		_, body := get(t, s.url+"/api/llm/status")
		_ = json.Unmarshal([]byte(body), &st)
		if st.Phase == phaseReady || st.Phase == phaseError {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if st.Phase != phaseReady {
		t.Fatalf("AI helper not ready: %+v", st)
	}
	if !strings.HasPrefix(st.Model, strings.Split(model, ":")[0]) {
		t.Fatalf("expected model %s, got %s", model, st.Model)
	}

	// Chat through the same-origin proxy exactly as the page does (with a browser Origin header).
	body := fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":"Reply with one word: hello"}],"stream":false}`, st.Model)
	req, _ := http.NewRequest(http.MethodPost, s.url+"/llm/api/chat", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", s.url)
	r, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var resp struct {
		Message struct{ Content string } `json:"message"`
	}
	raw, _ := io.ReadAll(r.Body)
	if r.StatusCode != 200 || json.Unmarshal(raw, &resp) != nil || strings.TrimSpace(resp.Message.Content) == "" {
		t.Fatalf("chat via proxy failed: %d %s", r.StatusCode, raw)
	}
	t.Logf("model replied: %q", resp.Message.Content)

	if dom := renderPage(t, s.url+"/"); dom != "" && !strings.Contains(dom, "AI helper is ready") {
		t.Error("page doesn't report the AI helper as ready")
	}
}

func TestInstallerInstallsWorkingBinary(t *testing.T) {
	// A fake "latest release": the freshly built binary under its release name, plus checksums.txt.
	rel := t.TempDir()
	asset := fmt.Sprintf("gcse-revision-%s-%s", runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		asset += ".exe"
	}
	b, _ := os.ReadFile(e2eBin)
	os.WriteFile(filepath.Join(rel, asset), b, 0o755)
	os.WriteFile(filepath.Join(rel, "checksums.txt"), []byte(sha256Hex(b)+"  "+asset+"\n"), 0o644)
	srv := httptest.NewServer(http.FileServer(http.Dir(rel)))
	defer srv.Close()

	binDir := t.TempDir()
	env := userEnv(t, "GCSE_REVISION_DOWNLOAD_BASE="+srv.URL, "GCSE_REVISION_NO_RUN=1", "GCSE_REVISION_NO_PATH=1", "GCSE_REVISION_BIN_DIR="+binDir)
	var cmd *exec.Cmd
	var installed string
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command",
			"Get-Content -Raw '"+filepath.Join(repoDir, "install.ps1")+"' | Invoke-Expression")
		installed = filepath.Join(envValue(env, "LOCALAPPDATA"), "Programs", "gcse-revision", "gcse-revision.exe")
	} else {
		cmd = exec.Command("sh", "-c", "cat '"+filepath.Join(repoDir, "install.sh")+"' | sh")
		installed = filepath.Join(binDir, "gcse-revision")
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	t.Logf("installer output:\n%s", out)
	if err != nil {
		t.Fatalf("installer failed: %v", err)
	}
	v, err := exec.Command(installed, "version").CombinedOutput()
	if err != nil || !strings.Contains(string(v), "e2e") {
		t.Fatalf("installed binary doesn't run: %v %s", err, v)
	}

	// A corrupted download must be refused.
	os.WriteFile(filepath.Join(rel, "checksums.txt"), []byte(strings.Repeat("0", 64)+"  "+asset+"\n"), 0o644)
	cmd2 := exec.Command(cmd.Path, cmd.Args[1:]...)
	cmd2.Env = env
	if out, err := cmd2.CombinedOutput(); err == nil {
		t.Fatalf("installer accepted a bad checksum:\n%s", out)
	}
}

func envValue(env []string, k string) string {
	for _, kv := range env {
		if strings.HasPrefix(kv, k+"=") {
			return strings.TrimPrefix(kv, k+"=")
		}
	}
	return ""
}
