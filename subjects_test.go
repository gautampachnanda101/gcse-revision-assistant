package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func contentZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for name, contents := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestLatestContentArchiveURLUsesNewestRelease(t *testing.T) {
	releases := []map[string]any{
		{
			"tag_name":     "content-20260101000000-old",
			"published_at": "2026-01-01T00:00:00Z",
			"assets": []map[string]any{{
				"name":                 "gcse-revision-content.zip",
				"browser_download_url": "https://example.invalid/old.zip",
			}},
		},
		{
			"tag_name":     "content-20260201000000-new",
			"published_at": "2026-02-01T00:00:00Z",
			"assets": []map[string]any{{
				"name":                 "gcse-revision-content.zip",
				"browser_download_url": "https://example.invalid/new.zip",
			}},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/me/repo/releases" {
			t.Errorf("unexpected request path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(releases)
	}))
	defer srv.Close()
	t.Setenv("GCSE_REVISION_API_BASE", srv.URL)

	got, err := latestContentArchiveURL(context.Background(), "me/repo")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.invalid/new.zip" {
		t.Fatalf("got %q, want newest release asset", got)
	}
}

func TestReadContentArchive(t *testing.T) {
	archive := contentZip(t, map[string][]byte{
		"subjects.js":     []byte("manifest"),
		sigFile:           []byte("signature"),
		"data/example.js": []byte("pack"),
	})
	files, err := readContentArchive(archive)
	if err != nil {
		t.Fatal(err)
	}
	if string(files["data/example.js"]) != "pack" {
		t.Fatalf("pack was not read: %q", files["data/example.js"])
	}
}

func TestReadContentArchiveRejectsUnsafeAndUnexpectedPaths(t *testing.T) {
	for _, name := range []string{"../subjects.js", "data/../subjects.js", "README.md"} {
		t.Run(name, func(t *testing.T) {
			archive := contentZip(t, map[string][]byte{
				name:          []byte("bad"),
				"subjects.js": []byte("manifest"),
				sigFile:       []byte("signature"),
			})
			if _, err := readContentArchive(archive); err == nil {
				t.Fatal("expected archive rejection")
			}
		})
	}
}

func TestReadContentArchiveRejectsOversizedFile(t *testing.T) {
	archive := contentZip(t, map[string][]byte{
		"subjects.js": []byte("manifest"),
		sigFile:       []byte("signature"),
	})
	signature := []byte{0x50, 0x4b, 0x01, 0x02}
	index := bytes.Index(archive, signature)
	if index < 0 || index+28 > len(archive) {
		t.Fatal("central directory not found")
	}
	binary.LittleEndian.PutUint32(archive[index+24:index+28], maxContentFileBytes+1)

	if _, err := readContentArchive(archive); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected size limit rejection, got %v", err)
	}
}

func TestSyncSubjectsUsesLegacyRawEndpointWhenConfigured(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	oldKey := contentPubKey
	contentPubKey = base64.StdEncoding.EncodeToString(pub)
	t.Cleanup(func() { contentPubKey = oldKey })

	manifestBytes := renderManifest(manifest{Schema: 1})
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, manifestBytes))
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/me/repo/mirror/subjects.js":
			_, _ = w.Write(manifestBytes)
		case "/me/repo/mirror/subjects.js.sig":
			_, _ = w.Write([]byte(signature))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("GCSE_REVISION_RAW_BASE", srv.URL)

	n, err := syncSubjects(context.Background(), "me/repo", "mirror", t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("downloaded %d packs, want 0", n)
	}
	if len(requests) != 2 {
		t.Fatalf("legacy mirror requests = %v", requests)
	}
}

func TestHTTPGetRejectsOversizedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("too large"))
	}))
	defer srv.Close()
	if _, err := httpGet(context.Background(), srv.URL, 4); err == nil {
		t.Fatal("expected response size rejection")
	}
}
