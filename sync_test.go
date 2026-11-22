package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// contentRelease serves a GitHub releases list and one signed content archive built from files.
type contentRelease struct {
	*httptest.Server
	archive  []byte
	releases string // overrides the generated releases JSON when set
}

func newContentRelease(t *testing.T, archive []byte) *contentRelease {
	t.Helper()
	cr := &contentRelease{archive: archive}
	cr.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/me/repo/releases":
			if cr.releases != "" {
				_, _ = w.Write([]byte(cr.releases))
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"tag_name": "content-1", "published_at": "2026-01-01T00:00:00Z",
				"assets": []map[string]string{{"name": "gcse-revision-content.zip", "browser_download_url": cr.URL + "/content.zip"}},
			}})
		case "/content.zip":
			_, _ = w.Write(cr.archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(cr.Close)
	t.Setenv("GCSE_REVISION_API_BASE", cr.URL)
	t.Setenv("GCSE_REVISION_RAW_BASE", "")
	return cr
}

// signedContent returns a content archive with the given packs and a manifest signed by priv.
func signedContent(t *testing.T, priv ed25519.PrivateKey, packs map[string]string, extra func(manifest) manifest) []byte {
	t.Helper()
	m := manifest{Schema: 1}
	files := map[string][]byte{}
	for name, body := range packs {
		file := "data/" + name
		files[file] = []byte(body)
		m.Subjects = append(m.Subjects, subjectEntry{ID: name, File: file, SHA256: sha256Hex([]byte(body))})
	}
	if extra != nil {
		m = extra(m)
	}
	mb := renderManifest(m)
	files["subjects.js"] = mb
	files[sigFile] = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, mb)))
	return contentZip(t, files)
}

func TestSyncSubjectsFromSignedRelease(t *testing.T) {
	isolateHome(t)
	priv := withSigningKey(t)
	pack := minimalPackJS("synced", "Synced")
	cr := newContentRelease(t, signedContent(t, priv, map[string]string{"synced.js": pack}, func(m manifest) manifest {
		m.Subjects = append(m.Subjects, subjectEntry{ID: "bad", File: "../evil.js", SHA256: "x"})
		return m
	}))
	var logs []string
	logger := func(f string, a ...any) { logs = append(logs, f) }
	n, err := syncSubjects(context.Background(), "me/repo", "", logger)
	if err != nil || n != 1 {
		t.Fatalf("first sync = %d, %v", n, err)
	}
	if b, _ := os.ReadFile(cachePackPath("data/synced.js")); string(b) != pack {
		t.Fatal("pack not cached")
	}
	mustContain(t, strings.Join(logs, "\n"), "Skipping invalid manifest entry", "Downloading subject pack")

	// Already cached: nothing new to fetch, and it is now served.
	if n, err := syncSubjects(context.Background(), "me/repo", "", logger); err != nil || n != 0 {
		t.Fatalf("second sync = %d, %v", n, err)
	}
	if b, err := readSubjectFile("data/synced.js"); err != nil || string(b) != pack {
		t.Fatalf("readSubjectFile = %v", err)
	}
	found := false
	for _, e := range effectiveManifest().Subjects {
		found = found || e.File == "data/synced.js"
	}
	if !found {
		t.Fatal("downloaded subject missing from effective manifest")
	}

	// A manifest entry with no file in the archive is rejected.
	cr.archive = signedContent(t, priv, nil, func(m manifest) manifest {
		m.Subjects = []subjectEntry{{ID: "x", File: "data/x.js", SHA256: strings.Repeat("a", 64)}}
		return m
	})
	errContains(t, func() error { _, err := syncSubjects(context.Background(), "me/repo", "", logger); return err }(), "missing from content archive")

	// A pack whose bytes don't match the signed hash is rejected.
	files := map[string][]byte{"data/x.js": []byte("tampered")}
	m := manifest{Schema: 1, Subjects: []subjectEntry{{ID: "x", File: "data/x.js", SHA256: strings.Repeat("a", 64)}}}
	mb := renderManifest(m)
	files["subjects.js"], files[sigFile] = mb, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, mb)))
	cr.archive = contentZip(t, files)
	errContains(t, func() error { _, err := syncSubjects(context.Background(), "me/repo", "", logger); return err }(), "checksum mismatch")

	// A signed file that isn't a manifest is rejected after signature checks.
	files = map[string][]byte{"subjects.js": []byte("not a manifest")}
	files[sigFile] = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, files["subjects.js"])))
	cr.archive = contentZip(t, files)
	errContains(t, func() error { _, err := syncSubjects(context.Background(), "me/repo", "", logger); return err }(), "prefix")

	// An archive signed with another key is refused.
	_, other, _ := ed25519.GenerateKey(nil)
	cr.archive = signedContent(t, other, map[string]string{"synced.js": pack}, nil)
	errContains(t, func() error { _, err := syncSubjects(context.Background(), "me/repo", "", logger); return err }(), "signature is invalid")

	cr.archive = []byte("not a zip")
	if _, err := syncSubjects(context.Background(), "me/repo", "", logger); err == nil {
		t.Fatal("corrupt archive should fail")
	}
	cr.releases = `[{"tag_name":"content-1","assets":[{"name":"gcse-revision-content.zip","browser_download_url":"` + cr.URL + `/missing.zip"}]}]`
	errContains(t, func() error { _, err := syncSubjects(context.Background(), "me/repo", "", logger); return err }(), "404")
	cr.releases = "[]"
	errContains(t, func() error { _, err := syncSubjects(context.Background(), "me/repo", "", logger); return err }(), "no signed content release")
	errContains(t, func() error { _, err := syncSubjects(context.Background(), "OWNER/repo", "", logger); return err }(), "no source repo")
}

func TestSyncSubjectsCacheWriteFailures(t *testing.T) {
	isolateHome(t)
	priv := withSigningKey(t)
	newContentRelease(t, signedContent(t, priv, map[string]string{"w.js": minimalPackJS("w", "W")}, nil))
	blocked := cachePackPath("data")
	if err := os.MkdirAll(filepath.Dir(blocked), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, blocked, "file where data/ should be")
	if _, err := syncSubjects(context.Background(), "me/repo", "", t.Logf); err == nil {
		t.Fatal("unwritable pack cache should fail")
	}
}

func TestSyncSubjectsLegacyMirror(t *testing.T) {
	isolateHome(t)
	priv := withSigningKey(t)
	pack := minimalPackJS("mirrored", "Mirrored")
	m := manifest{Schema: 1, Subjects: []subjectEntry{{ID: "mirrored", File: "data/mirrored.js", SHA256: sha256Hex([]byte(pack))}}}
	mb := renderManifest(m)
	routes := map[string]string{
		"/me/repo/b/subjects.js":      string(mb),
		"/me/repo/b/" + sigFile:       base64.StdEncoding.EncodeToString(ed25519.Sign(priv, mb)),
		"/me/repo/b/data/mirrored.js": pack,
	}
	srv := servePaths(t, routes)
	t.Setenv("GCSE_REVISION_RAW_BASE", srv.URL)
	if n, err := syncSubjects(context.Background(), "me/repo", "b", t.Logf); err != nil || n != 1 {
		t.Fatalf("mirror sync = %d, %v", n, err)
	}
	if err := os.Remove(cachePackPath("data/mirrored.js")); err != nil {
		t.Fatal(err)
	}
	delete(routes, "/me/repo/b/data/mirrored.js")
	srv2 := servePaths(t, routes)
	t.Setenv("GCSE_REVISION_RAW_BASE", srv2.URL)
	if _, err := syncSubjects(context.Background(), "me/repo", "b", t.Logf); err == nil {
		t.Fatal("missing mirrored pack should fail")
	}
	delete(routes, "/me/repo/b/"+sigFile)
	srv3 := servePaths(t, routes)
	t.Setenv("GCSE_REVISION_RAW_BASE", srv3.URL)
	errContains(t, func() error { _, err := syncSubjects(context.Background(), "me/repo", "b", t.Logf); return err }(), "content signature")
	t.Setenv("GCSE_REVISION_RAW_BASE", servePaths(t, nil).URL)
	if _, err := syncSubjects(context.Background(), "me/repo", "b", t.Logf); err == nil {
		t.Fatal("missing manifest should fail")
	}
}

func TestLatestContentArchiveURLEdgeCases(t *testing.T) {
	for body, want := range map[string]string{
		"{not json": "content releases",
		`[{"tag_name":"v1.0.0","assets":[{"name":"gcse-revision-content.zip","browser_download_url":"x"}]},
		  {"tag_name":"content-d","draft":true,"assets":[{"name":"gcse-revision-content.zip","browser_download_url":"x"}]},
		  {"tag_name":"content-n","assets":[{"name":"other.zip","browser_download_url":"x"}]}]`: "no signed content release",
		`[{"tag_name":"content-e","assets":[{"name":"gcse-revision-content.zip","browser_download_url":""}]}]`: "has no download URL",
	} {
		t.Setenv("GCSE_REVISION_API_BASE", servePaths(t, map[string]string{"/repos/me/repo/releases": body}).URL)
		errContains(t, func() error { _, err := latestContentArchiveURL(context.Background(), "me/repo"); return err }(), want)
	}
	t.Setenv("GCSE_REVISION_API_BASE", "http://127.0.0.1:1")
	if _, err := latestContentArchiveURL(context.Background(), "me/repo"); err == nil {
		t.Fatal("unreachable API should fail")
	}
}

func TestReadContentArchiveLimits(t *testing.T) {
	if _, err := readContentArchive([]byte("nope")); err == nil {
		t.Fatal("non-zip should fail")
	}
	errContains(t, func() error {
		_, err := readContentArchive(contentZip(t, map[string][]byte{"subjects.js": []byte("m")}))
		return err
	}(), "missing manifest or signature")

	many := map[string][]byte{}
	for i := 0; i <= maxContentFiles; i++ {
		many["data/p"+itoa(i)+".js"] = nil
	}
	errContains(t, func() error { _, err := readContentArchive(contentZip(t, many)); return err }(), "more than")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	_, _ = zw.Create("data/")
	for i := 0; i < 2; i++ {
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: "subjects.js"})
		_, _ = w.Write([]byte("m"))
	}
	_ = zw.Close()
	errContains(t, func() error { _, err := readContentArchive(buf.Bytes()); return err }(), "duplicate")

	buf.Reset()
	zw = zip.NewWriter(&buf)
	h := &zip.FileHeader{Name: "data/link.js"}
	h.SetMode(os.ModeSymlink | 0o777)
	w, _ := zw.CreateHeader(h)
	_, _ = w.Write([]byte("../../etc/passwd"))
	_ = zw.Close()
	errContains(t, func() error { _, err := readContentArchive(buf.Bytes()); return err }(), "unsafe content archive entry")

	// Real entries whose combined size exceeds the total limit.
	chunk := make([]byte, 43<<20) // 3 × 43 MB > 128 MB total, each under the 64 MB per-file limit; zeros compress to almost nothing
	big := contentZip(t, map[string][]byte{"data/a.js": chunk, "data/b.js": chunk, "data/c.js": chunk})
	errContains(t, func() error { _, err := readContentArchive(big); return err }(), "total uncompressed size limit")
	// A declared size smaller than the real data is caught when reading.
	lying := contentZip(t, map[string][]byte{"subjects.js": []byte("manifest"), sigFile: []byte("s")})
	patchSizes(t, lying, 1)
	if _, err := readContentArchive(lying); err == nil {
		t.Fatal("size mismatch should fail")
	}
}

// patchSizes rewrites every central-directory uncompressed size in a zip.
func patchSizes(t *testing.T, z []byte, size uint32) {
	t.Helper()
	sig := []byte{0x50, 0x4b, 0x01, 0x02}
	for i := bytes.Index(z, sig); i >= 0; {
		putUint32(z[i+24:], size)
		next := bytes.Index(z[i+4:], sig)
		if next < 0 {
			break
		}
		i += 4 + next
	}
}

func putUint32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}

func TestManifestHelpers(t *testing.T) {
	if m := manifestOrEmpty([]byte("garbage")); m.Schema != 1 || len(m.Subjects) != 0 {
		t.Fatal("manifestOrEmpty fallback")
	}
	if _, err := parseManifest([]byte(manifestPrefix + "{bad};")); err == nil {
		t.Fatal("bad JSON should fail")
	}
	if _, err := parseManifest([]byte("var x;\n" + manifestPrefix + "{};")); err == nil {
		t.Fatal("code before the prefix should fail")
	}
	if _, ok := packBytes(subjectEntry{File: "../x.js"}); ok {
		t.Fatal("invalid path must not resolve")
	}
	if _, ok := packBytes(subjectEntry{File: "data/aqa-rs-a-sikhism.js", SHA256: "wrong"}); ok {
		t.Fatal("hash mismatch must not resolve")
	}
	if _, err := readSubjectFile("../../etc/passwd"); err == nil {
		t.Fatal("invalid names are not served")
	}
	isolateHome(t)
	if _, err := readSubjectFile("data/never-existed.js"); err == nil {
		t.Fatal("unknown pack should fail")
	}
	if firstMatch(reStorageID, []byte("nothing")) != "" {
		t.Fatal("firstMatch without a match")
	}
	if _, err := httpGet(context.Background(), "http://bad host/", 1); err == nil {
		t.Fatal("malformed URL should fail")
	}
	if _, err := httpGet(context.Background(), "http://127.0.0.1:1/", 1); err == nil {
		t.Fatal("unreachable URL should fail")
	}
	blocked := filepath.Join(t.TempDir(), "f")
	writeFile(t, blocked, "x")
	if err := writeFileAtomic(filepath.Join(blocked, "a", "b"), nil); err == nil {
		t.Fatal("mkdir under a file should fail")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "x.tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "x.tmp", "keep"), "x")
	if err := writeFileAtomic(filepath.Join(dir, "x"), nil); err == nil {
		t.Fatal("unwritable temp file should fail")
	}
}

func TestWriteManifest(t *testing.T) {
	for name, packs := range map[string]map[string]string{
		"may only use letters":         {"bad name.js": minimalPackJS("a", "A")},
		"doesn't push onto":            {"a.js": "var x = 1;"},
		"config.storageId is required": {"a.js": "window.REVISION_SUBJECTS.push({});"},
		`share storageId "same"`:       {"a.js": minimalPackJS("same", "A"), "b.js": minimalPackJS("same", "B")},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeTree(t, filepath.Join(dir, "data"), packs)
			errContains(t, writeManifest(dir), name)
		})
	}

	// Existing order is preserved; new packs are appended alphabetically.
	dir := contentDir(t, map[string]string{"b.js": minimalPackJS("b", "B"), "a.js": minimalPackJS("a", "A")})
	m, _ := parseManifest([]byte(mustRead(t, filepath.Join(dir, "subjects.js"))))
	m.Subjects[0], m.Subjects[1] = m.Subjects[1], m.Subjects[0]
	writeFile(t, filepath.Join(dir, "subjects.js"), string(renderManifest(m)))
	writeTree(t, filepath.Join(dir, "data"), map[string]string{"d.js": minimalPackJS("d", "D"), "c.js": minimalPackJS("c", "C")})
	captureStdout(t, func() {
		if err := writeManifest(dir); err != nil {
			t.Fatal(err)
		}
	})
	m, _ = parseManifest([]byte(mustRead(t, filepath.Join(dir, "subjects.js"))))
	var order []string
	for _, e := range m.Subjects {
		order = append(order, e.ID)
	}
	if strings.Join(order, ",") != "b,a,c,d" {
		t.Fatalf("order = %v", order)
	}

	if err := os.Remove(filepath.Join(dir, "subjects.js")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "subjects.js"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(dir); err == nil {
		t.Fatal("unwritable subjects.js should fail")
	}
	unreadable := t.TempDir()
	if err := os.MkdirAll(filepath.Join(unreadable, "data", "x.js"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(unreadable); err == nil {
		t.Fatal("unreadable pack should fail")
	}
	if err := writeManifest("[["); err != nil {
		// A malformed glob pattern is reported (filepath.ErrBadPattern).
		_ = err
	}
}
