package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testHost = "127.0.0.1:8765"
const testLocalToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func testHandler(t *testing.T, host string, ai *aiManager) http.Handler {
	t.Helper()
	return newHandler(host, ai, newCloudAIManagerAt(filepath.Join(t.TempDir(), "cloud-ai.json"), newMemoryCredentialStore()), testLocalToken)
}

func do(t *testing.T, h http.Handler, method, target string, body io.Reader, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://"+testHost+target, body)
	req.Host = testHost
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

// exportedBundle returns a small valid open-format bundle built from a shipped pack.
func exportedBundle(t *testing.T) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "x.pack.json")
	if err := exportPack("data/aqa-rs-a-sikhism.js", out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHandlerHostAndOriginGuards(t *testing.T) {
	isolateHome(t)
	h := testHandler(t, testHost, newAIManager("", false))
	for _, host := range []string{testHost, "localhost:8765", "gcse-revision.localhost:8765"} {
		req := httptest.NewRequest(http.MethodGet, "/api/info", nil)
		req.Host = host
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("host %s: %d", host, res.Code)
		}
		if res.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
			t.Fatal("CORP header missing")
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/info", nil)
	req.Host = "evil.example:8765" // DNS rebinding
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("foreign host = %d", res.Code)
	}
	if r := do(t, h, http.MethodPost, "/api/import", strings.NewReader("{}"), map[string]string{"Origin": "http://evil.example"}); r.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST = %d", r.Code)
	}
	if r := do(t, h, http.MethodPost, "/api/import", strings.NewReader("{}"), map[string]string{"Origin": "http://localhost:9999"}); r.Code != http.StatusForbidden {
		t.Fatalf("other localhost port POST = %d", r.Code)
	}
	if r := do(t, h, http.MethodGet, "/api/info", nil, map[string]string{"Sec-Fetch-Site": "cross-site"}); r.Code != http.StatusForbidden {
		t.Fatalf("cross-site fetch = %d", r.Code)
	}
}

func TestHandlerRoutes(t *testing.T) {
	isolateHome(t)
	withOS(t, "linux", "amd64") // no Apple bridge
	f := newFakeOllama(t, "llama3.2")
	ai := newAIManager("", false)
	h := testHandler(t, testHost, ai)

	var info map[string]any
	r := do(t, h, http.MethodGet, "/api/info", nil, nil)
	if json.Unmarshal(r.Body.Bytes(), &info) != nil || info["app"] != appName || info["version"] != version {
		t.Fatalf("/api/info = %s", r.Body.String())
	}
	if r.Header().Get("Cache-Control") != "no-store" || r.Header().Get("Content-Type") != "application/json" {
		t.Fatal("writeJSON headers missing")
	}
	if r := do(t, h, http.MethodGet, "/api/llm/status", nil, nil); !strings.Contains(r.Body.String(), `"phase":"idle"`) {
		t.Fatalf("/api/llm/status = %s", r.Body.String())
	}
	if r := do(t, h, http.MethodGet, "/api/llm/builtin", nil, nil); !strings.Contains(r.Body.String(), `"available":false`) {
		t.Fatalf("/api/llm/builtin = %s", r.Body.String())
	}

	// Built-in chat.
	if r := do(t, h, http.MethodGet, "/llm/builtin/chat/completions", nil, nil); r.Code != http.StatusMethodNotAllowed {
		t.Fatalf("chat GET = %d", r.Code)
	}
	if r := do(t, h, http.MethodPost, "/llm/builtin/chat/completions", strings.NewReader("{bad"), nil); r.Code != http.StatusBadRequest {
		t.Fatalf("chat bad JSON = %d", r.Code)
	}
	if r := do(t, h, http.MethodPost, "/llm/builtin/chat/completions", strings.NewReader(`{"messages":[]}`), nil); r.Code != http.StatusServiceUnavailable {
		t.Fatalf("chat without bridge = %d", r.Code)
	}
	withOS(t, "darwin", "arm64")
	installFakeExec(t, func(string) fakeResult { return ok(`{"model":"apple","content":"Well done"}`) })
	r = do(t, h, http.MethodPost, "/llm/builtin/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`), nil)
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "Well done") || !strings.Contains(r.Body.String(), `"role":"assistant"`) {
		t.Fatalf("chat = %d %s", r.Code, r.Body.String())
	}
	withOS(t, "linux", "amd64")

	// AI setup.
	if r := do(t, h, http.MethodGet, "/api/llm/setup", nil, nil); r.Code != http.StatusMethodNotAllowed {
		t.Fatalf("setup GET = %d", r.Code)
	}
	ai.disable()
	if r := do(t, h, http.MethodPost, "/api/llm/setup", nil, nil); r.Code != http.StatusOK {
		t.Fatalf("setup POST = %d", r.Code)
	}
	deadline := time.Now().Add(5 * time.Second)
	for ai.status().Phase != phaseReady {
		if time.Now().After(deadline) {
			t.Fatalf("AI never became ready: %+v", ai.status())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r := do(t, h, http.MethodGet, "/llm/api/version", nil, nil); r.Code != http.StatusOK {
		t.Fatalf("/llm proxy = %d", r.Code)
	}
	_ = f

	// Import.
	if r := do(t, h, http.MethodGet, "/api/import", nil, nil); r.Code != http.StatusMethodNotAllowed {
		t.Fatalf("import GET = %d", r.Code)
	}
	if r := do(t, h, http.MethodPost, "/api/import", strings.NewReader("nope"), nil); r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "error") {
		t.Fatalf("bad import = %d %s", r.Code, r.Body.String())
	}
	r = do(t, h, http.MethodPost, "/api/import", strings.NewReader(string(exportedBundle(t))), map[string]string{"Origin": "http://" + testHost})
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"questions"`) {
		t.Fatalf("good import = %d %s", r.Code, r.Body.String())
	}

	// Static content.
	r = do(t, h, http.MethodGet, "/subjects.js", nil, nil)
	if !strings.Contains(r.Body.String(), manifestPrefix) || !strings.Contains(r.Body.String(), "data/private/") {
		t.Fatalf("/subjects.js should list built-in and imported packs: %.200s", r.Body.String())
	}
	if r := do(t, h, http.MethodGet, "/data/aqa-rs-a-sikhism.js", nil, nil); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "REVISION_SUBJECTS") {
		t.Fatalf("/data pack = %d", r.Code)
	}
	if r := do(t, h, http.MethodGet, "/data/nope.js", nil, nil); r.Code != http.StatusNotFound {
		t.Fatalf("/data missing = %d", r.Code)
	}
	for _, p := range []string{"/", "/app.html", "/index.html"} {
		if r := do(t, h, http.MethodGet, p, nil, nil); r.Code != http.StatusOK || !strings.Contains(r.Header().Get("Content-Type"), "text/html") {
			t.Fatalf("%s = %d", p, r.Code)
		} else if strings.Contains(r.Body.String(), "__GCSE_REVISION_LOCAL_TOKEN__") || !strings.Contains(r.Body.String(), testLocalToken) {
			t.Fatalf("%s did not receive a live local session token", p)
		}
	}
	if r := do(t, h, http.MethodGet, "/secret.go", nil, nil); r.Code != http.StatusNotFound {
		t.Fatalf("unknown path = %d", r.Code)
	}
	if r := do(t, h, http.MethodGet, "/audio/x/y.mp3", nil, nil); r.Code != http.StatusNotFound {
		t.Fatalf("missing audio = %d", r.Code)
	}
}

func TestServeContextLifecycle(t *testing.T) {
	isolateHome(t)
	withOS(t, "linux", "amd64")
	newFakeOllama(t, "llama3.2")
	fx := installFakeExec(t, func(string) fakeResult { return ok("") })
	releases := servePaths(t, map[string]string{"/repos/me/repo/releases": "[]"})
	t.Setenv("GCSE_REVISION_API_BASE", releases.URL)

	port := unusedPort(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	out := captureStdout(t, func() {
		go func() {
			done <- serveContext(ctx, options{port: port, repo: "me/repo", noAutoUp: true, service: true})
		}()
		deadline := time.Now().Add(5 * time.Second)
		for !alreadyRunning(net.JoinHostPort("127.0.0.1", itoa(port))) {
			if time.Now().After(deadline) {
				t.Fatal("server never came up")
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serveContext = %v", err)
		}
	})
	mustContain(t, out, "Subject update skipped", "is running at", "Background service ready")
	if !fx.called("xdg-open http://gcse-revision.localhost:") {
		t.Fatalf("browser not opened: %v", fx.Calls())
	}
	if b, _ := os.ReadFile(serviceLogPath()); !strings.Contains(string(b), "is running at") {
		t.Fatal("service mode must also log to the service log")
	}
}

func TestServeContextPicksFreePortWhenBusy(t *testing.T) {
	isolateHome(t)
	busy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") }))
	defer busy.Close()
	port := busy.Listener.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithCancel(context.Background())
	out := captureStdout(t, func() {
		go func() { time.Sleep(200 * time.Millisecond); cancel() }()
		if err := serveContext(ctx, options{port: port, noBrowser: true, offline: true, noAI: true, noAutoUp: true}); err != nil {
			t.Error(err)
		}
	})
	mustContain(t, out, "is busy; picking a free one", "Keep this window open")
}

func TestServeExistingInstanceOpensBrowser(t *testing.T) {
	isolateHome(t)
	withOS(t, "darwin", "arm64")
	port := fakeApp(t, version)
	fx := installFakeExec(t, func(string) fakeResult { return ok("") })
	captureStdout(t, func() {
		if err := serve(options{port: port, offline: true, noAI: true, noAutoUp: true}); err != nil {
			t.Fatal(err)
		}
	})
	if !fx.called("open http://gcse-revision.localhost:") {
		t.Fatalf("calls = %v", fx.Calls())
	}
}

func TestServeServiceLoggingFailure(t *testing.T) {
	isolateHome(t)
	writeFile(t, serviceDir(), "file in the way")
	if err := serve(options{port: unusedPort(t), service: true}); err == nil {
		t.Fatal("service logging failure should stop serve")
	}
}

func TestOpenBrowserPerPlatform(t *testing.T) {
	for goos, want := range map[string]string{"darwin": "open http://x/", "windows": "rundll32 url.dll,FileProtocolHandler http://x/", "linux": "xdg-open http://x/"} {
		withOS(t, goos, "amd64")
		fx := installFakeExec(t, func(string) fakeResult { return ok("") })
		openBrowser("http://x/")
		if !fx.called(want) {
			t.Fatalf("%s: calls = %v", goos, fx.Calls())
		}
	}
	setVar(t, &execCommand, func(string, ...string) *exec.Cmd { return exec.Command(filepath.Join(t.TempDir(), "nope")) })
	out := captureStdout(t, func() { openBrowser("http://x/") })
	mustContain(t, out, "Open http://x/ in your browser")
}

func TestSmallMainHelpers(t *testing.T) {
	t.Setenv("GCSE_TEST_ENV", "  v  ")
	if envOr("GCSE_TEST_ENV", "d") != "v" || envOr("GCSE_TEST_UNSET_ENV", "d") != "d" {
		t.Fatal("envOr")
	}
	if alreadyRunning(net.JoinHostPort("127.0.0.1", itoa(unusedPort(t)))) {
		t.Fatal("nothing is listening")
	}
	// dataDir falls back to the temp dir when there is no user cache dir.
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("LocalAppData", "")
	t.Setenv("LOCALAPPDATA", "")
	if d := dataDir(); !strings.Contains(d, appName) {
		t.Fatalf("dataDir fallback = %q", d)
	}
}
