package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportImportRoundTrip(t *testing.T) {
	t.Setenv("GCSE_REVISION_PRIVATE_PACKS", t.TempDir())
	bundle := filepath.Join(t.TempDir(), "sikhism.pack.json")
	if err := exportPack("data/aqa-rs-a-sikhism.js", bundle); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	res, err := importBytes(b)
	if err != nil {
		t.Fatalf("import of exported bundle: %v", err)
	}
	if res.Questions == 0 || res.Modules == 0 {
		t.Fatalf("imported pack looks empty: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(privatePacksDir(), res.ID+".js")); err != nil {
		t.Fatalf("pack not installed: %v", err)
	}
}

func TestImportRejectsInvalidPack(t *testing.T) {
	t.Setenv("GCSE_REVISION_PRIVATE_PACKS", t.TempDir())
	for name, in := range map[string]string{
		"not json":       `nope`,
		"missing config": `{"modules":[]}`,
		"wrong type":     `{"config":"x","modules":[]}`,
	} {
		if _, err := importBytes([]byte(in)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestImportZipWithAudio(t *testing.T) {
	t.Setenv("GCSE_REVISION_PRIVATE_PACKS", t.TempDir())
	src := t.TempDir()
	write := func(name, body string) {
		p := filepath.Join(src, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := os.ReadFile("content/french/pack.json")
	write("pack.json", strings.Replace(string(b), "aqa_french", "test_audio", 1))
	m, _ := os.ReadFile("content/french/modules/p1-1.1.json")
	write("modules/p1-1.1.json", strings.Replace(string(m), `"answerTopic": "1.1-listening-0"`, `"audio": "clip1.mp3", "answerTopic": "1.1-listening-0"`, 1))
	if _, err := os.ReadFile("content/french/modules/p1-1.1.json"); err != nil {
		t.Fatal(err)
	}
	if err := importPaths([]string{src}); err == nil {
		t.Fatal("expected missing-audio error")
	}
	write("audio/clip1.mp3", "ID3")
	if err := importPaths([]string{src}); err != nil {
		t.Fatalf("import with audio: %v", err)
	}
	if _, err := os.Stat(filepath.Join(audioDir("test-audio"), "clip1.mp3")); err != nil {
		t.Fatalf("audio not installed: %v", err)
	}
	exported := filepath.Join(t.TempDir(), "roundtrip.zip")
	installed, err := os.ReadFile(filepath.Join(privatePacksDir(), "test-audio.js"))
	if err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(t.TempDir(), "renamed-pack.js")
	if err := os.WriteFile(renamed, installed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := exportPack(renamed, exported); err != nil {
		t.Fatalf("export pack with audio: %v", err)
	}
	packZip, err := os.ReadFile(exported)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := importBytes(packZip); err != nil {
		t.Fatalf("reimport exported audio pack: %v", err)
	}
	if _, err := os.Stat(filepath.Join(audioDir("test-audio"), "clip1.mp3")); err != nil {
		t.Fatalf("round-trip lost audio: %v", err)
	}
}

func TestPrivateFolderIsNotTracked(t *testing.T) {
	out, err := exec.Command("git", "ls-files", "private").Output()
	if err != nil {
		t.Skip("not a git checkout")
	}
	for _, f := range strings.Fields(string(out)) {
		if f != "private/README.md" {
			t.Errorf("%s is tracked by git; private/ must stay local and gitignored", f)
		}
	}
}

func TestPrivateResourcesRejectCrossOriginGets(t *testing.T) {
	h := testHandler(t, "127.0.0.1:8765", newAIManager("", false))
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/subjects.js", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("cross-origin manifest status = %d, want %d", res.Code, http.StatusForbidden)
	}

	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/subjects.js", nil)
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if got := res.Header().Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
		t.Fatalf("Cross-Origin-Resource-Policy = %q", got)
	}
}

func TestBuiltInAppleAIEndpointShape(t *testing.T) {
	h := testHandler(t, "127.0.0.1:8765", newAIManager("", false))
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8765/api/llm/builtin", nil)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	var body struct {
		Available bool   `json:"available"`
		Provider  string `json:"provider"`
		Kind      string `json:"kind"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Kind != "apple-foundation-models" {
		t.Fatalf("built-in AI kind = %q", body.Kind)
	}
	_ = body.Available
	_ = body.Provider
}
