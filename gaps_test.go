package main

// Failure-path tests: each one drives a specific error branch (I/O failures, truncated downloads, rollback
// after a failed install) so that the behaviour on failure is verified, not just the happy path.

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
)

// skipIfPermissionsIgnored skips chmod-based tests where permissions don't restrict the current user.
func skipIfPermissionsIgnored(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions are not enforced for this user/OS")
	}
}

// failRename makes the rename seam fail for calls matching pred.
func failRename(t *testing.T, pred func(src, dst string) bool) {
	t.Helper()
	setVar(t, &rename, func(src, dst string) error {
		if pred(src, dst) {
			return errors.New("injected rename failure")
		}
		return os.Rename(src, dst)
	})
}

// rawZipEntry builds a zip whose single entry uses an unsupported compression method, so Open fails.
func rawZipEntry(t *testing.T, name string, extra map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, b := range extra {
		w, _ := zw.Create(n)
		_, _ = w.Write([]byte(b))
	}
	w, err := zw.CreateRaw(&zip.FileHeader{Name: name, Method: 99, CompressedSize64: 1, UncompressedSize64: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("x"))
	_ = zw.Close()
	return buf.Bytes()
}

func TestInstallRollsBackOnEveryFailure(t *testing.T) {
	isolateHome(t)
	src := sourcePack("t_rb", "B", "Languages")
	src["modules/1.1.json"] = strings.Replace(src["modules/1.1.json"], `"marks":2`, `"marks":2,"audio":"a.mp3"`, 1)
	src["audio/a.mp3"] = "v1"
	id, p, audio, err := loadPackBytes(zipOf(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if err := installPrivatePack(id, p, audio); err != nil {
		t.Fatal(err)
	}
	finalPack := filepath.Join(privatePacksDir(), id+".js")
	original := mustRead(t, finalPack)
	next := audioOf(audio, "v2")

	for name, pred := range map[string]func(src, dst string) bool{
		"staging the pack":     func(_, dst string) bool { return strings.Contains(dst, ".import-") && filepath.Base(dst) == id+".js" },
		"staging the audio":    func(_, dst string) bool { return strings.Contains(dst, ".import-") && filepath.Base(dst) == "a.mp3" },
		"backing up the pack":  func(_, dst string) bool { return filepath.Base(dst) == "previous.js" },
		"installing the pack":  func(src, dst string) bool { return dst == finalPack && filepath.Base(src) == id+".js" },
		"backing up the audio": func(_, dst string) bool { return filepath.Base(dst) == "previous-audio" },
		"installing the audio": func(src, _ string) bool { return strings.Contains(src, ".import-") && filepath.Base(src) == "audio" },
	} {
		t.Run(name, func(t *testing.T) {
			failRename(t, pred)
			errContains(t, installPrivatePack(id, p, next), "injected rename failure")
			setVar(t, &rename, os.Rename)
			if got := mustRead(t, finalPack); got != original {
				t.Fatal("the previously installed pack was not restored")
			}
			if b, err := os.ReadFile(filepath.Join(audioDir(id), "a.mp3")); err != nil || string(b) != "v1" {
				t.Fatalf("the previously installed recording was not restored: %q %v", b, err)
			}
		})
	}

	// The audio folder's parent can't be created: a fresh install (no previous audio) rolls the pack back out.
	fresh := sourcePack("t_rb2", "B", "Languages")
	fresh["modules/1.1.json"] = src["modules/1.1.json"]
	fresh["audio/a.mp3"] = "v1"
	id2, p2, a2, err := loadPackBytes(zipOf(t, fresh))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(privatePacksDir(), "audio")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(privatePacksDir(), "audio"), "file in the way")
	if err := installPrivatePack(id2, p2, a2); err == nil {
		t.Fatal("expected the audio mkdir to fail")
	}
	if _, err := os.Stat(filepath.Join(privatePacksDir(), id2+".js")); !os.IsNotExist(err) {
		t.Fatal("a half-installed pack was left behind")
	}
}

func audioOf(a packAudio, body string) packAudio {
	out := packAudio{}
	for k := range a {
		out[k] = []byte(body)
	}
	return out
}

func TestInstallAndLockPermissionFailures(t *testing.T) {
	skipIfPermissionsIgnored(t)
	isolateHome(t)
	src := sourcePack("t_perm", "B", "Languages")
	id, p, audio, err := loadPackBytes(zipOf(t, src))
	if err != nil {
		t.Fatal(err)
	}
	if err := installPrivatePack(id, p, audio); err != nil {
		t.Fatal(err)
	}
	dir := privatePacksDir()
	if err := os.Chmod(dir, 0o500); err != nil { // .locks still writable, staging dir can't be made
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := installPrivatePack(id, p, audio); err == nil {
		t.Fatal("expected the staging directory to fail")
	}
	_ = os.Chmod(dir, 0o755)
	locks := filepath.Join(dir, ".locks")
	if err := os.Chmod(locks, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locks, 0o700) })
	if _, err := lockPrivateInstall(id); err == nil || strings.Contains(err.Error(), "still installing") {
		t.Fatalf("expected a permission error, got %v", err)
	}
	_ = os.Chmod(locks, 0o700)

	// importBytes reports install failures; importPaths reports unreadable files.
	t.Setenv("GCSE_REVISION_PRIVATE_PACKS", filepath.Join(mustTempFile(t), "packs"))
	if _, err := importBytes(zipOf(t, src)); err == nil {
		t.Fatal("install failure should propagate from importBytes")
	}
	unreadable := filepath.Join(t.TempDir(), "pack.json")
	writeFile(t, unreadable, "{}")
	if err := os.Chmod(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	if err := importPaths([]string{unreadable}); err == nil {
		t.Fatal("unreadable file should fail")
	}
}

func mustTempFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "file")
	writeFile(t, p, "x")
	return p
}

func TestSchemaValidButUndecodableSources(t *testing.T) {
	src := sourcePack("t_dec", "B", "Languages")
	src["pack.json"] = strings.Replace(src["pack.json"], `"meta":`, `"questions":"not a map","meta":`, 1)
	if _, _, err := readSourceFS(mapFS(src)); err == nil || !strings.Contains(err.Error(), "pack.json") {
		t.Fatalf("pack.json decode error = %v", err)
	}
	src = sourcePack("t_dec", "B", "Languages")
	huge := mapFS(src)
	huge["modules/2.json"] = &fstest.MapFile{Data: bytes.Repeat([]byte(" "), maxImportBytes+1)}
	errContains(t, func() error { _, _, err := readSourceFS(huge); return err }(), "too large")

	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "embedded pack schema is invalid") {
			t.Fatalf("mustParseSchema should panic on a broken schema, got %v", r)
		}
	}()
	mustParseSchema([]byte("{"))
}

func TestExportEdgeCases(t *testing.T) {
	isolateHome(t)
	p := packFile{
		Config:    map[string]any{"storageId": "t_x", "examBoard": "B", "subjectArea": "A", "subjectName": "S", "brandTitle": "T", "papers": []any{map[string]any{"id": "P1", "label": "P1", "title": "Paper 1", "subtitle": "s"}}},
		Notes:     []packNote{{Paper: "P1", Module: "1", Summary: "s"}},
		Questions: map[string][]packQuestion{"paper1": {{ID: "q", Paper: "P1", Module: "1", ModuleTitle: "From question", Question: "q", ModelAnswer: "a", Marks: 1, Command: "Give"}}},
	}
	dst := filepath.Join(t.TempDir(), "x.pack.json")
	if err := exportParsedPack(p, dst); err != nil {
		t.Fatal(err)
	}
	mustContain(t, mustRead(t, dst), `"moduleTitle": "From question"`)
	if strings.Contains(mustRead(t, dst), "null") {
		t.Fatal("exports must omit absent optional fields rather than write null")
	}

	unmarshalable := p
	unmarshalable.Config = map[string]any{"bad": func() {}}
	if err := exportParsedPack(unmarshalable, dst); err == nil {
		t.Fatal("unmarshalable config should fail")
	}
	if err := exportAudioZip(unmarshalable, nil, dst); err == nil {
		t.Fatal("unmarshalable base pack should fail")
	}
	if err := exportAudioZip(p, []*exportModule{{Questions: []map[string]any{{"bad": func() {}}}}}, dst); err == nil {
		t.Fatal("unmarshalable module should fail")
	}
}

func TestValidateExplicitFiles(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken.js")
	writeFile(t, broken, "not a pack")
	warn := filepath.Join(dir, "warn.js")
	pk := packFile{
		Config:    map[string]any{"storageId": "w", "examBoard": "B", "subjectArea": "A", "subjectName": "S", "brandTitle": "T", "papers": []any{map[string]any{"id": "P1"}}},
		Questions: map[string][]packQuestion{"paper1": {{ID: "q", Paper: "P1", Module: "1", Question: "q", ModelAnswer: "a", Marks: 1, Command: "Give"}}},
	}
	writeFile(t, warn, string(renderPackJS(pk)))
	out := captureStdout(t, func() {
		errContains(t, validateFiles([]string{broken, warn}), "1 pack(s) failed")
	})
	mustContain(t, out, "FAIL "+broken, "expected window.REVISION_SUBJECTS.push", "warning: P1 1 has questions but no revision note")
}

func TestCryptoFailures(t *testing.T) {
	isolateHome(t)
	setVar(t, &privatePackKey, "")
	setVar(t, &randReader, io.Reader(failingReader{}))
	key, enc := testKey(t)
	if _, err := encryptPack(key, []byte("x")); err == nil {
		t.Fatal("nonce generation failure should propagate")
	}
	t.Setenv("GCSE_PRIVATE_KEY", enc)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "private", "a.js"), "x")
	if err := encryptPrivate(dir); err == nil {
		t.Fatal("encryption failure should propagate")
	}
}

func TestNetworkTruncationFailures(t *testing.T) {
	short := shortBody(t)
	if _, err := httpGet(context.Background(), short, 1<<20); err == nil {
		t.Fatal("truncated body should fail httpGet")
	}
	if _, err := downloadBytes(context.Background(), short, 1<<20); err == nil {
		t.Fatal("truncated body should fail downloadBytes")
	}
	setVar(t, &githubAPIBase, strings.TrimSuffix(short, "/short"))
	shortAll := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("{"))
	}))
	defer shortAll.Close()
	setVar(t, &githubAPIBase, shortAll.URL)
	if _, err := latestRelease(context.Background()); err == nil {
		t.Fatal("truncated release JSON should fail")
	}
	if _, err := readContentArchive(rawZipEntry(t, "data/x.js", map[string]string{"subjects.js": "m", sigFile: "s"})); err == nil {
		t.Fatal("unsupported compression should fail to open")
	}
	z := rawZipEntry(t, "ollama", nil)
	if err := unzip(bytes.NewReader(z), int64(len(z)), t.TempDir()); err == nil {
		t.Fatal("unsupported compression should fail to unzip")
	}
	a := newAIManager("", true)
	t.Setenv("OLLAMA_HOST", "127.0.0.1:1")
	if newAIManager("", true).serverUp(context.Background()) {
		t.Fatal("nothing is listening")
	}
	_ = a
}

func TestUpgradeInstallFailures(t *testing.T) {
	isolateHome(t)
	withOS(t, "linux", "amd64")
	setVar(t, &version, "v1.0.0")
	exe := standaloneExe(t)
	newReleaseServer(t, "v1.1.0", []byte("new"))
	writeFile(t, filepath.Join(exe+".new", "keep"), "x") // staging path is a non-empty dir
	if _, err := selfUpgrade(context.Background(), false); err == nil {
		t.Fatal("staging failure should propagate")
	}
	if err := os.RemoveAll(exe + ".new"); err != nil {
		t.Fatal(err)
	}
	withOS(t, "linux", "amd64")
	setVar(t, &chmod, func(string, os.FileMode) error { return errors.New("injected chmod failure") })
	errContains(t, installUpgrade(exe, []byte("x"), false), "injected chmod failure")
	if _, err := os.Stat(exe + ".new"); !os.IsNotExist(err) {
		t.Fatal("staged binary left behind after chmod failure")
	}
}

func TestOllamaInstallerFailures(t *testing.T) {
	isolateHome(t)
	withOS(t, "darwin", "arm64")
	ollamaRelease(t, "ollama-darwin.tgz", gz(t, makeTar(t, ollamaTar)), "")
	a := newAIManager("", true)

	func() {
		tmp := os.TempDir()
		blockTempDir(t)
		defer func() {
			for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
				t.Setenv(k, tmp)
			}
		}()
		if _, err := a.installOllama(context.Background()); err == nil {
			t.Fatal("temp file failure should propagate")
		}
	}()

	blockCacheDir(t)
	captureStdout(t, func() {
		if _, err := a.installOllama(context.Background()); err == nil {
			t.Fatal("unpack dir failure should propagate")
		}
	})
	isolateHome(t)

	withOS(t, "linux", "amd64")
	setVar(t, &lookPath, func(name string) (string, error) { return "/usr/bin/" + name, nil })
	ollamaRelease(t, "ollama-linux-amd64.tar.zst", []byte("zst"), "")
	setVar(t, &execCommandContext, func(ctx context.Context, name string, args ...string) *exec.Cmd {
		c := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperProcess$")
		c.Stdout = io.Discard // StdoutPipe refuses a command whose Stdout is already set
		return c
	})
	captureStdout(t, func() {
		errContains(t, func() error { _, err := a.installOllama(context.Background()); return err }(), "Stdout already set")
	})

	// The official installer ran but left no ollama anywhere we look.
	setVar(t, &systemOllamaPaths, func() []string { return nil })
	setVar(t, &lookPath, func(name string) (string, error) {
		if name == "curl" {
			return "/usr/bin/curl", nil
		}
		return "", errors.New("not found")
	})
	installFakeExec(t, func(string) fakeResult { return ok("") })
	errContains(t, func() error { _, err := a.installOllamaScript(context.Background()); return err }(), "installer finished but ollama wasn't found")
	if findOllama() != "" {
		t.Fatal("no ollama should be found")
	}

	setVar(t, &systemOllamaPaths, systemOllamaPathsDefault)
	withOS(t, "windows", "amd64")
	t.Setenv("LOCALAPPDATA", "")
	if paths := systemOllamaPaths(); paths != nil {
		t.Fatalf("windows without LOCALAPPDATA = %v", paths)
	}
	withOS(t, "darwin", "arm64")
	if paths := systemOllamaPaths(); len(paths) != 4 || !strings.HasPrefix(paths[0], "/Applications/Ollama.app") {
		t.Fatalf("darwin paths = %v", paths)
	}
	withOS(t, "linux", "amd64")
	if paths := systemOllamaPaths(); len(paths) != 2 {
		t.Fatalf("linux paths = %v", paths)
	}
}

// fakeListener fails Accept immediately and reports a non-TCP address.
type fakeListener struct{}

func (fakeListener) Accept() (net.Conn, error) { return nil, errors.New("injected accept failure") }
func (fakeListener) Close() error              { return nil }
func (fakeListener) Addr() net.Addr            { return &net.UnixAddr{Name: "fake", Net: "unix"} }

func TestServeContextFailuresAndSync(t *testing.T) {
	isolateHome(t)
	setVar(t, &listen, func(string, string) (net.Listener, error) { return nil, errors.New("injected listen failure") })
	captureStdout(t, func() {
		errContains(t, serveContext(context.Background(), options{port: unusedPort(t), noBrowser: true, offline: true, noAI: true, noAutoUp: true}), "injected listen failure")
	})

	setVar(t, &listen, func(string, string) (net.Listener, error) { return fakeListener{}, nil })
	priv := withSigningKey(t)
	newContentRelease(t, signedContent(t, priv, map[string]string{"served.js": minimalPackJS("served", "Served")}, nil))
	setVar(t, &version, "dev") // auto-upgrade loop starts and returns immediately for dev builds
	out := captureStdout(t, func() {
		errContains(t, serveContext(context.Background(), options{port: 8765, noBrowser: true, noAI: true, repo: "me/repo"}), "injected accept failure")
	})
	mustContain(t, out, "Downloaded 1 new/updated subject pack(s)", "gcse-revision.localhost:8765")
}

func TestServiceHomeLookupFailures(t *testing.T) {
	serviceEnv(t, "darwin")
	installFakeExec(t, func(string) fakeResult { return ok("") })
	setVar(t, &currentUser, func() (*user.User, error) { return &user.User{Uid: "501", Username: "tester"}, nil })
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "")
	if _, err := os.UserHomeDir(); err == nil {
		t.Skip("this platform always reports a home directory")
	}
	if homebrewServiceConfigured() {
		t.Fatal("no home: not Homebrew-managed")
	}
	for _, goos := range []string{"darwin", "linux"} {
		withOS(t, goos, "amd64")
		if _, err := serviceConfigured(); err == nil {
			t.Fatalf("%s serviceConfigured without home should fail", goos)
		}
		if err := installService(options{port: 1}); err == nil {
			t.Fatalf("%s install without home should fail", goos)
		}
	}
}

func TestAudioTotalLimit(t *testing.T) {
	fsys := mapFS(sourcePack("t", "B", "L"))
	chunk := make([]byte, 22<<20) // each under the per-file import limit; together over the 64 MB audio limit
	for _, n := range []string{"a.mp3", "b.mp3", "c.mp3"} {
		fsys["audio/"+n] = &fstest.MapFile{Data: chunk}
	}
	errContains(t, func() error { _, err := readAudio(fsys); return err }(), "64 MB total limit")
}

func TestUndecodableModulesAndBundles(t *testing.T) {
	// The schema doesn't constrain question.id (the builder assigns it), so a numeric id passes validation but
	// can't be decoded into the string field.
	src := sourcePack("t_id", "B", "Languages")
	src["modules/1.1.json"] = strings.Replace(src["modules/1.1.json"], `"marks":2`, `"marks":2,"id":5`, 1)
	errContains(t, func() error { _, _, err := readSourceFS(mapFS(src)); return err }(), "modules/1.1.json")

	bundle := `{"config":` + strings.TrimSuffix(strings.SplitN(src["pack.json"], `"config":`, 2)[1], `,"meta":{"title":"T"}}`) +
		`,"modules":[` + src["modules/1.1.json"] + `]}`
	if _, _, err := readSourceBundle([]byte(bundle)); err == nil || strings.Contains(err.Error(), "bundle $") {
		t.Fatalf("expected a decode (not schema) error, got %v", err)
	}
}

func TestInstallServiceDarwinPlistWriteFailure(t *testing.T) {
	serviceEnv(t, "darwin")
	installFakeExec(t, func(string) fakeResult { return ok("") })
	failRename(t, func(_, dst string) bool { return strings.HasSuffix(dst, ".plist") })
	errContains(t, installService(options{port: 1}), "injected rename failure")
}

func TestBakedPrivatePacksAreListedAndOverridable(t *testing.T) {
	isolateHome(t)
	setVar(t, &bakedPrivatePacks, func() map[string][]byte {
		return map[string][]byte{"baked.js": []byte(minimalPackJS("baked", "Baked in")), "shadow.js": []byte(minimalPackJS("shadow", "Baked"))}
	})
	writeFile(t, filepath.Join(privatePacksDir(), "shadow.js"), minimalPackJS("shadow", "From disk"))
	titles := map[string]string{}
	for _, e := range privatePacks() {
		titles[e.ID] = e.Title
	}
	if titles["baked"] != "Baked in" || titles["shadow"] != "From disk" {
		t.Fatalf("baked packs must be listed and the on-disk folder must win: %v", titles)
	}
}

func TestInstallServiceDirFailure(t *testing.T) {
	serviceEnv(t, "linux")
	writeFile(t, serviceDir(), "file where the service folder should be")
	if err := installService(options{port: 1}); err == nil {
		t.Fatal("expected the service folder creation to fail")
	}
}
