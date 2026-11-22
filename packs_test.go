package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// sourcePack returns a minimal valid open-format source (pack.json + one module) as files.
func sourcePack(storageID, board, area string) map[string]string {
	cfg := map[string]any{
		"storageId": storageID, "examBoard": board, "subjectArea": area, "subjectName": "Test",
		"brandTitle": "Test " + storageID,
		"papers":     []map[string]string{{"id": "Paper 1", "label": "P1", "title": "Paper one", "subtitle": "s"}},
	}
	pack, _ := json.Marshal(map[string]any{"config": cfg, "meta": map[string]any{"title": "T"}})
	mod, _ := json.Marshal(map[string]any{
		"paper": "Paper 1", "module": "1.1", "moduleTitle": "Intro",
		"note":      map[string]string{"specFocus": "focus", "summary": "summary text"},
		"questions": []map[string]any{{"question": "Explain one thing.", "marks": 2, "command": "Explain", "modelAnswer": "Because."}},
	})
	return map[string]string{"pack.json": string(pack), "modules/1.1.json": string(mod)}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		writeFile(t, filepath.Join(root, filepath.FromSlash(name)), body)
	}
}

func mapFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for k, v := range files {
		m[k] = &fstest.MapFile{Data: []byte(v)}
	}
	return m
}

func zipOf(t *testing.T, files map[string]string) []byte {
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

func TestParseAndValidatePack(t *testing.T) {
	if _, err := parsePack([]byte("var x = 1;")); err == nil {
		t.Fatal("missing push call should fail")
	}
	if _, err := parsePack([]byte(packPush + "{not json});")); err == nil {
		t.Fatal("bad JSON body should fail")
	}
	probs := strings.Join(validatePack(packFile{}), "\n")
	mustContain(t, probs, "config.storageId is required", "config.papers must list at least one paper", "pack has no questions")

	p := packFile{
		Config: map[string]any{"storageId": "Bad-ID", "examBoard": "B", "subjectArea": "A", "subjectName": "S", "brandTitle": "T",
			"papers": []any{map[string]any{"id": "P1"}}},
		Questions: map[string][]packQuestion{"paper1": {
			{ID: "a", Paper: "P1", Module: "1", Question: "q", ModelAnswer: "a", Marks: 1, Command: "Give"},
			{ID: "a", Paper: "P9", Module: "", Question: " ", ModelAnswer: "", Marks: 0},
		}},
		Notes: []packNote{{ID: "n"}},
	}
	probs = strings.Join(validatePack(p), "\n")
	mustContain(t, probs, "lowercase letters", "id missing or duplicated", `paper "P9" isn't in config.papers`,
		"question and modelAnswer are required", "marks, command and module are required", "paper, module and summary are required")

	w := packWarnings(packFile{
		Notes:     []packNote{{Paper: "*", Module: "2"}},
		Questions: map[string][]packQuestion{"paper1": {{Paper: "P1", Module: "1"}, {Paper: "P1", Module: "1"}, {Paper: "P1", Module: "2"}}},
	})
	if len(w) != 1 || !strings.Contains(w[0], "P1 1 has questions but no revision note") {
		t.Fatalf("warnings = %v", w)
	}
	if cfgString(map[string]any{"n": 1}, "n") != "" {
		t.Fatal("non-string config values read as empty")
	}
}

func TestNaturalLess(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"1.2", "1.10", true}, {"1.10", "1.2", false}, {"Theme A", "Theme B", true},
		{"1", "1.1", true}, {"1.1", "1.1", false}, {"A.1", "1.1", false},
	} {
		if got := naturalLess(tc.a, tc.b); got != tc.want {
			t.Errorf("naturalLess(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}

func TestAssemblePack(t *testing.T) {
	src := sourcePack("rs_test", "Pearson Edexcel", "Religious Studies")
	base, mods, err := readSourceFS(mapFS(src))
	if err != nil {
		t.Fatal(err)
	}
	out, err := assemblePack("rs_test", base, mods)
	if err != nil {
		t.Fatal(err)
	}
	if out.MarkingSchema["id"] != "edexcel-rs-generic" || out.Config["markingSchemaId"] != "edexcel-rs-generic" {
		t.Fatalf("Edexcel RS packs get the adaptable Edexcel schema: %v", out.MarkingSchema["id"])
	}
	if base.Config["markingSchemaId"] != nil {
		t.Fatal("assemblePack must not mutate the caller's config")
	}
	if out.MarkingSchema["status"] != "adaptable" {
		t.Fatal("board-derived schemas must be marked adaptable (AGENTS.md §5)")
	}
	q := out.Questions["paper1"][0]
	if q.ID != "rs-test-paper1-1-1-0" || q.Evidence == nil || out.Notes[0].SourcePages == "" || out.Meta["questionTotal"] != 1 {
		t.Fatalf("assembled question = %+v meta=%v", q, out.Meta)
	}

	base.Meta = map[string]any{"idPrefix": "legacy"}
	out, _ = assemblePack("rs_test", base, mods)
	if got := out.Questions["paper1"][0].ID; got != "legacy-p1-1-1-0" {
		t.Fatalf("idPrefix keeps legacy ids stable, got %q", got)
	}

	base.Config["examBoard"] = "OCR"
	if out, _ := assemblePack("rs_test", base, mods); out.MarkingSchema != nil {
		t.Fatal("unknown boards get no default schema")
	}
	if defaultReligiousStudiesMarkingSchema("AQA GCSE")["id"] != "aqa-rs-generic" {
		t.Fatal("AQA default schema")
	}

	bad := append([]moduleSource(nil), mods...)
	bad[0].Paper = "Paper 9"
	errContains(t, func() error { _, err := assemblePack("rs_test", base, bad); return err }(), "isn't one of config.papers")
	bad[0].Paper = "Paper 1"
	bad[0].Questions = []packQuestion{{Question: "q", ModelAnswer: "a"}}
	errContains(t, func() error { _, err := assemblePack("rs_test", base, bad); return err }(), "marks, command and module")
	if m := cloneAnyMap(map[string]any{"a": 1}); m["a"] != 1 {
		t.Fatal("cloneAnyMap")
	}
}

func TestReadSourceFS(t *testing.T) {
	src := sourcePack("t1", "B", "Languages")
	nested := fstest.MapFS{"__MACOSX/pack.json": {Data: []byte("{}")}, ".git/pack.json": {Data: []byte("{}")}, "README": {Data: []byte("x")}}
	for k, v := range src {
		nested["pack-1/"+k] = &fstest.MapFile{Data: []byte(v)}
	}
	if _, mods, err := readSourceFS(nested); err != nil || len(mods) != 1 {
		t.Fatalf("single nested folder: %v", err)
	}
	two := fstest.MapFS{"a/pack.json": {Data: []byte("{}")}, "b/pack.json": {Data: []byte("{}")}}
	errContains(t, func() error { _, _, err := readSourceFS(two); return err }(), "expected pack.json")

	for name, mutate := range map[string]func(map[string]string){
		"pack.json not JSON": func(m map[string]string) { m["pack.json"] = "{" },
		"pack.json schema":   func(m map[string]string) { m["pack.json"] = `{"config":{}}` },
		"no modules":         func(m map[string]string) { delete(m, "modules/1.1.json") },
		"module schema":      func(m map[string]string) { m["modules/1.1.json"] = `{"paper":1}` },
		"pack.json bad shape": func(m map[string]string) {
			m["pack.json"] = strings.Replace(m["pack.json"], `"meta":{"title":"T"}`, `"meta":{"title":"T"},"rubrics":{"x":5}`, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := sourcePack("t1", "B", "Languages")
			mutate(m)
			if _, _, err := readSourceFS(mapFS(m)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
	huge := mapFS(src)
	huge["pack.json"] = &fstest.MapFile{Data: bytes.Repeat([]byte(" "), maxImportBytes+1)}
	errContains(t, func() error { _, _, err := readSourceFS(huge); return err }(), "too large")
	if _, err := readLimited(mapFS(src), "missing.json"); err == nil {
		t.Fatal("missing file should fail")
	}
}

func TestLoadPackBytesFormats(t *testing.T) {
	src := sourcePack("t_zip", "B", "Languages")
	if id, _, _, err := loadPackBytes(zipOf(t, src)); err != nil || id != "t-zip" {
		t.Fatalf("zip: %q %v", id, err)
	}
	withAudio := sourcePack("t_audio", "B", "Languages")
	withAudio["modules/1.1.json"] = strings.Replace(withAudio["modules/1.1.json"], `"marks":2`, `"marks":2,"audio":"clip.mp3"`, 1)
	if _, _, _, err := loadPackBytes(zipOf(t, withAudio)); err == nil || !strings.Contains(err.Error(), "clip.mp3") {
		t.Fatalf("missing referenced audio should fail, got %v", err)
	}
	withAudio["audio/clip.mp3"] = "ID3"
	if _, _, a, err := loadPackBytes(zipOf(t, withAudio)); err != nil || len(a) != 1 {
		t.Fatalf("zip with audio: %v", err)
	}
	withAudio["audio/bad name.mp3"] = "x"
	if _, _, _, err := loadPackBytes(zipOf(t, withAudio)); err == nil {
		t.Fatal("invalid audio file name should fail")
	}
	if _, _, _, err := loadPackBytes([]byte("PK\x03\x04 truncated")); err == nil {
		t.Fatal("corrupt zip should fail")
	}
	many := map[string]string{}
	for i := 0; i <= maxZipEntries; i++ {
		many["f"+itoa(i)] = ""
	}
	errContains(t, func() error { _, _, _, err := loadPackBytes(zipOf(t, many)); return err }(), "zip is too large")
	if _, _, _, err := loadPackBytes(zipOf(t, map[string]string{"x.txt": "x"})); err == nil {
		t.Fatal("zip without pack.json should fail")
	}

	built := mustRead(t, "data/aqa-rs-a-sikhism.js")
	if id, _, _, err := loadPackBytes([]byte(built)); err != nil || id != "aqa-rs-a-sikhism" {
		t.Fatalf("built pack: %q %v", id, err)
	}
	broken := `window.REVISION_SUBJECTS.push({"config":{}});`
	if _, _, _, err := loadPackBytes([]byte(broken)); err == nil {
		t.Fatal("invalid built pack should fail")
	}
	if _, _, _, err := loadPackBytes([]byte(`{"config":{},"modules":[]}`)); err == nil {
		t.Fatal("invalid bundle should fail")
	}
}

func TestInstallPrivatePack(t *testing.T) {
	isolateHome(t)
	src := sourcePack("t_inst", "B", "Languages")
	src["modules/1.1.json"] = strings.Replace(src["modules/1.1.json"], `"marks":2`, `"marks":2,"audio":"a.mp3"`, 1)
	src["audio/a.mp3"] = "first"
	id, p, audio, err := loadPackBytes(zipOf(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if err := installPrivatePack(id, p, audio); err != nil {
		t.Fatal(err)
	}
	// Reinstall replaces both the pack and its recordings.
	audio["a.mp3"] = []byte("second")
	if err := installPrivatePack(id, p, audio); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(audioDir(id), "a.mp3")); string(b) != "second" {
		t.Fatalf("audio not replaced: %q", b)
	}

	// The installed recording is served with a safe content type.
	res := httptest.NewRecorder()
	serveAudio(res, httptest.NewRequest(http.MethodGet, "/audio/"+id+"/a.mp3", nil))
	if res.Code != http.StatusOK || res.Header().Get("Content-Type") != "audio/mpeg" || res.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("serveAudio = %d %v", res.Code, res.Header())
	}
	for _, bad := range []string{"/audio/" + id + "/../x.mp3", "/audio/" + id + "/a.exe", "/audio/only"} {
		res := httptest.NewRecorder()
		serveAudio(res, httptest.NewRequest(http.MethodGet, bad, nil))
		if res.Code != http.StatusNotFound {
			t.Fatalf("%s = %d", bad, res.Code)
		}
	}

	errContains(t, installPrivatePack("bad id", p, audio), "use letters, digits")
	errContains(t, installPrivatePack(id, p, packAudio{}), "a.mp3")
	if _, err := stageAudio(t.TempDir(), "bad id", audio); err == nil {
		t.Fatal("invalid pack id for audio should fail")
	}

	// A stale lock left by a crashed import is taken over; a fresh one times out.
	setVar(t, &privateInstallLockWait, 60*time.Millisecond)
	lock := filepath.Join(privatePacksDir(), ".locks", id+".lock")
	writeFile(t, lock, "999")
	old := time.Now().Add(-2 * time.Minute)
	_ = os.Chtimes(lock, old, old)
	if err := installPrivatePack(id, p, audio); err != nil {
		t.Fatalf("stale lock not recovered: %v", err)
	}
	writeFile(t, lock, "999")
	errContains(t, installPrivatePack(id, p, audio), "still installing")
	_ = os.Remove(lock)

	// The private folder can't be created.
	blocked := filepath.Join(t.TempDir(), "file")
	writeFile(t, blocked, "x")
	t.Setenv("GCSE_REVISION_PRIVATE_PACKS", filepath.Join(blocked, "packs"))
	if err := installPrivatePack(id, p, audio); err == nil {
		t.Fatal("expected a mkdir failure")
	}
	if _, err := importBytes([]byte("nope")); err == nil {
		t.Fatal("bad import bytes should fail")
	}
}

func TestLockPrivateInstallMkdirFailure(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file")
	writeFile(t, blocked, "x")
	t.Setenv("GCSE_REVISION_PRIVATE_PACKS", blocked)
	if _, err := lockPrivateInstall("x"); err == nil {
		t.Fatal("expected a lock dir failure")
	}
}

func TestImportPaths(t *testing.T) {
	isolateHome(t)
	dir := filepath.Join(t.TempDir(), "pack")
	writeTree(t, dir, sourcePack("t_dir", "B", "Languages"))
	out := captureStdout(t, func() {
		if err := importPaths([]string{dir}); err != nil {
			t.Fatal(err)
		}
	})
	mustContain(t, out, "Imported Test t_dir", "Restart the app")

	bad := filepath.Join(t.TempDir(), "bad")
	writeTree(t, bad, map[string]string{"pack.json": "{}"})
	if err := importPaths([]string{bad}); err == nil {
		t.Fatal("invalid folder should fail")
	}
	mismatch := filepath.Join(t.TempDir(), "mismatch")
	m := sourcePack("t_mm", "B", "Languages")
	m["modules/1.1.json"] = strings.Replace(m["modules/1.1.json"], `"paper":"Paper 1"`, `"paper":"Paper 7"`, 1)
	writeTree(t, mismatch, m)
	if err := importPaths([]string{mismatch}); err == nil {
		t.Fatal("assembly failure should fail")
	}
	audioDirPack := filepath.Join(t.TempDir(), "audio")
	a := sourcePack("t_aud", "B", "Languages")
	a["modules/1.1.json"] = strings.Replace(a["modules/1.1.json"], `"marks":2`, `"marks":2,"audio":"missing.mp3"`, 1)
	writeTree(t, audioDirPack, a)
	errContains(t, importPaths([]string{audioDirPack}), "missing.mp3")

	if err := importPaths([]string{filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("missing path should fail")
	}
	junk := filepath.Join(t.TempDir(), "junk.json")
	writeFile(t, junk, "{}")
	if err := importPaths([]string{junk}); err == nil {
		t.Fatal("invalid bundle file should fail")
	}
	t.Setenv("GCSE_REVISION_PRIVATE_PACKS", junk) // a file: install fails
	if err := importPaths([]string{dir}); err == nil {
		t.Fatal("install failure should fail")
	}
}

func TestExportPack(t *testing.T) {
	isolateHome(t)
	if err := exportPack(filepath.Join(t.TempDir(), "missing.js"), "x"); err == nil {
		t.Fatal("missing source should fail")
	}
	junk := filepath.Join(t.TempDir(), "junk.js")
	writeFile(t, junk, "nope")
	if err := exportPack(junk, "x"); err == nil {
		t.Fatal("unparseable source should fail")
	}

	// A pack whose questions have no matching note still exports one module per (paper, module).
	src := sourcePack("t_exp", "AQA GCSE", "Religious Studies")
	_, p, _, err := loadPackBytes(zipOf(t, src))
	if err != nil {
		t.Fatal(err)
	}
	p.Notes = nil
	js := filepath.Join(t.TempDir(), "t.js")
	writeFile(t, js, string(renderPackJS(p)))
	dst := filepath.Join(t.TempDir(), "t.pack.json")
	if err := exportPack(js, dst); err != nil {
		t.Fatal(err)
	}
	mustContain(t, mustRead(t, dst), `"moduleTitle": "Intro"`, `"markingSchema"`)

	// Packs with recordings must be exported as zips that carry the audio.
	src["modules/1.1.json"] = strings.Replace(src["modules/1.1.json"], `"marks":2`, `"marks":2,"audio":"a.mp3"`, 1)
	src["audio/a.mp3"] = "ID3"
	res, err := importBytes(zipOf(t, src))
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(privatePacksDir(), res.ID+".js")
	errContains(t, exportPack(installed, filepath.Join(t.TempDir(), "x.pack.json")), "export it to a .zip")
	zipDst := filepath.Join(t.TempDir(), "x.zip")
	if err := exportPack(installed, zipDst); err != nil {
		t.Fatal(err)
	}
	zb, _ := os.ReadFile(zipDst)
	if _, _, a, err := loadPackBytes(zb); err != nil || string(a["a.mp3"]) != "ID3" {
		t.Fatalf("exported zip doesn't round-trip: %v", err)
	}
	if err := os.Remove(filepath.Join(audioDir(res.ID), "a.mp3")); err != nil {
		t.Fatal(err)
	}
	errContains(t, exportPack(installed, zipDst), `read audio "a.mp3"`)

	// Invalid exports are refused by the schema check.
	bad := packFile{Config: map[string]any{"storageId": 5}, Questions: map[string][]packQuestion{}}
	writeFile(t, js, string(renderPackJS(bad)))
	if err := exportPack(js, dst); err == nil {
		t.Fatal("schema-invalid export should fail")
	}
}

func TestBuildPack(t *testing.T) {
	work := t.TempDir()
	chdir(t, work)
	errContains(t, buildPack(filepath.Join(work, "bad name"), work), "letters, digits")
	errContains(t, buildPack(filepath.Join(work, "missing"), work), "expected pack.json")

	src := filepath.Join(work, "content", "t-build")
	writeTree(t, src, sourcePack("t_build", "B", "Languages"))
	out := captureStdout(t, func() {
		if err := buildPack(src, work); err != nil {
			t.Fatal(err)
		}
	})
	mustContain(t, out, "Built", "1 questions across 1 modules")

	// validateFiles (full mode): every data pack must be generated from its content folder.
	out = captureStdout(t, func() {
		if err := validateFiles(nil); err != nil {
			t.Fatal(err)
		}
	})
	mustContain(t, out, "ok   "+filepath.Join("data", "t-build.js"), "ok   "+filepath.Join("content", "t-build"))

	writeFile(t, filepath.Join(work, "data", "t-build.js"), mustRead(t, filepath.Join(work, "data", "t-build.js"))+"\n// edited by hand\n")
	writeFile(t, filepath.Join(work, "data", "orphan.js"), minimalPackJS("orphan", "Orphan"))
	writeTree(t, filepath.Join(work, "content", "broken"), map[string]string{"pack.json": "{}"})
	out = captureStdout(t, func() {
		errContains(t, validateFiles(nil), "failed validation")
	})
	mustContain(t, out, "out of date", "FAIL "+filepath.Join("data", "orphan.js")+" is not generated", "FAIL "+filepath.Join("content", "broken"), "config.papers must list")
	if err := validateFiles([]string{filepath.Join(work, "nope.js")}); err == nil {
		t.Fatal("missing explicit file should fail")
	}

	mm := filepath.Join(work, "content", "t-mm")
	m := sourcePack("t_mm", "B", "Languages")
	m["modules/1.1.json"] = strings.Replace(m["modules/1.1.json"], `"paper":"Paper 1"`, `"paper":"Paper 7"`, 1)
	writeTree(t, mm, m)
	if err := buildPack(mm, work); err == nil {
		t.Fatal("assembly failure should fail")
	}
	au := filepath.Join(work, "content", "t-au")
	a := sourcePack("t_au", "B", "Languages")
	a["modules/1.1.json"] = strings.Replace(a["modules/1.1.json"], `"marks":2`, `"marks":2,"audio":"x.mp3"`, 1)
	writeTree(t, au, a)
	errContains(t, buildPack(au, work), "only supported for imported (private) packs")

	blocked := filepath.Join(work, "blockedout")
	writeFile(t, blocked, "file")
	if err := buildPack(src, blocked); err == nil {
		t.Fatal("unwritable output should fail")
	}
}

func TestPrivatePackDiscovery(t *testing.T) {
	isolateHome(t)
	dir := privatePacksDir()
	writeFile(t, filepath.Join(dir, "mine.js"), minimalPackJS("mine", "Mine"))
	writeFile(t, filepath.Join(dir, "bad name.js"), minimalPackJS("bad", "Bad"))
	writeFile(t, filepath.Join(dir, "noid.js"), "window.REVISION_SUBJECTS.push({});")
	var found bool
	for _, e := range privatePacks() {
		switch e.File {
		case "data/private/mine.js":
			found = e.ID == "mine" && e.Title == "Mine" && len(e.SHA256) == 64
		case "data/private/bad name.js", "data/private/noid.js":
			t.Fatalf("invalid private pack listed: %s", e.File)
		}
	}
	if !found {
		t.Fatal("private pack not listed")
	}
	if b, err := readPrivatePack("data/private/mine.js"); err != nil || !strings.Contains(string(b), "mine") {
		t.Fatalf("readPrivatePack = %v", err)
	}
	if _, err := readPrivatePack("data/private/absent.js"); err == nil {
		t.Fatal("absent private pack should not be found")
	}
	if b, err := readSubjectFile("data/private/mine.js"); err != nil || len(b) == 0 {
		t.Fatal("readSubjectFile should delegate private packs")
	}

	t.Setenv("GCSE_REVISION_PRIVATE_PACKS", "")
	if !strings.Contains(privatePacksDir(), appName) {
		t.Fatalf("default private dir = %q", privatePacksDir())
	}
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("APPDATA", "")
	if !strings.Contains(privatePacksDir(), appName) {
		t.Fatalf("fallback private dir = %q", privatePacksDir())
	}
}

func TestAudioReading(t *testing.T) {
	big := mapFS(sourcePack("t", "B", "L"))
	big["audio/a.mp3"] = &fstest.MapFile{Data: make([]byte, maxAudioBytes/2+1)}
	big["audio/b.mp3"] = &fstest.MapFile{Data: make([]byte, maxAudioBytes/2+1)}
	errContains(t, func() error { _, err := readAudio(big); return err }(), "too large")
	if _, err := readAudio(fstest.MapFS{}); err == nil {
		t.Fatal("no pack root should fail")
	}
	huge := mapFS(sourcePack("t", "B", "L"))
	huge["audio/h.mp3"] = &fstest.MapFile{Data: make([]byte, maxImportBytes+1)}
	if _, err := readAudio(huge); err == nil {
		t.Fatal("oversized single file should fail")
	}
	blocked := filepath.Join(t.TempDir(), "f")
	writeFile(t, blocked, "x")
	if _, err := stageAudio(blocked, "ok", packAudio{"a.mp3": nil}); err == nil {
		t.Fatal("unwritable stage should fail")
	}
	stage := t.TempDir()
	if err := os.MkdirAll(filepath.Join(stage, "audio", "a.mp3"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(stage, "audio", "a.mp3", "x"), "x")
	if _, err := stageAudio(stage, "ok", packAudio{"a.mp3": []byte("x")}); err == nil {
		t.Fatal("rename onto a non-empty dir should fail")
	}
}

func TestSchemaValidator(t *testing.T) {
	if _, err := parseSchema([]byte("{")); err == nil {
		t.Fatal("bad schema JSON should fail")
	}
	s, err := parseSchema([]byte(`{
	  "$defs": {
	    "obj": {"type":"object","required":["s"],"additionalProperties":false,
	      "properties":{
	        "s":{"type":"string","minLength":2,"maxLength":3,"pattern":"^[a-z]+$"},
	        "n":{"type":["number","null"],"minimum":1,"maximum":5},
	        "e":{"enum":["x","y"]},
	        "c":{"const":"fixed"},
	        "b":{"type":"boolean"},
	        "a":{"type":"array","minItems":1,"items":{"type":"integer"}},
	        "r":{"$ref":"#/$defs/missing"},
	        "m":{"type":"object","additionalProperties":{"type":"string"}}
	      }},
	    "notobj": 5
	  }
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if probs := s.validate("#/$defs/obj", map[string]any{"s": "ab", "n": nil, "e": "x", "c": "fixed", "b": true, "a": []any{1.0}, "m": map[string]any{"k": "v"}}); len(probs) != 0 {
		t.Fatalf("valid document rejected: %v", probs)
	}
	probs := strings.Join(s.validate("#/$defs/obj", map[string]any{
		"n": 9.5, "e": "z", "c": "other", "b": "no", "a": []any{}, "r": 1.0, "m": map[string]any{"k": 1.0}, "extra": 1.0,
	}), "\n")
	mustContain(t, probs, `missing required property "s"`, "must be at most 5", "must be one of", "must be fixed",
		"expected boolean, got string", "needs at least 1 item", `bad $ref "#/$defs/missing"`, "expected string, got integer", `unexpected property "extra"`)
	probs = strings.Join(s.validate("#/$defs/obj", map[string]any{"s": "A", "n": 0.0, "a": []any{1.5}}), "\n")
	mustContain(t, probs, "at least 2 character", "doesn't match", "must be at least 1", "expected integer, got number")
	mustContain(t, strings.Join(s.validate("#/$defs/obj", map[string]any{"s": "abcd"}), "\n"), "at most 3 characters")
	mustContain(t, strings.Join(s.validate("http://x", nil), "\n"), "unsupported $ref")
	mustContain(t, strings.Join(s.validate("#/$defs/notobj", nil), "\n"), "bad $ref")
	mustContain(t, strings.Join(s.validate("#/$defs/notobj/deeper", nil), "\n"), "bad $ref")
	if len(s.validate("", map[string]any{})) != 0 {
		t.Fatal("root schema accepts an empty object")
	}
	for v, want := range map[any]string{nil: "null", true: "boolean", 1.5: "number", 2.0: "integer", "s": "string", int8(1): "unknown"} {
		if got := jsonType(v); got != want {
			t.Errorf("jsonType(%v) = %s", v, got)
		}
	}
	if probs := schemaProblems("#", "x", []byte("{")); len(probs) != 1 || !strings.Contains(probs[0], "not valid JSON") {
		t.Fatalf("schemaProblems = %v", probs)
	}
}
