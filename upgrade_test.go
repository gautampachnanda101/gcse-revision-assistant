package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// releaseServer fakes the GitHub "latest release" API plus its asset downloads.
type releaseServer struct {
	*httptest.Server
	tag      string
	binary   []byte
	sums     string // checksums.txt body; computed from binary when empty
	omit     string // asset name to leave out of the release
	status   int    // non-zero → /releases/latest answers with this status
	hits     atomic.Int32
	assetErr string // asset name that answers 500
}

func newReleaseServer(t *testing.T, tag string, binary []byte) *releaseServer {
	t.Helper()
	rs := &releaseServer{tag: tag, binary: binary}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asset := upgradeAssetName()
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			rs.hits.Add(1)
			if rs.status != 0 {
				w.WriteHeader(rs.status)
				return
			}
			var assets []map[string]string
			for _, name := range []string{asset, "checksums.txt"} {
				if name != rs.omit {
					assets = append(assets, map[string]string{"name": name, "browser_download_url": rs.URL + "/dl/" + name})
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": rs.tag, "assets": assets})
		case r.URL.Path == "/dl/"+rs.assetErr:
			w.WriteHeader(http.StatusInternalServerError)
		case r.URL.Path == "/dl/checksums.txt":
			sums := rs.sums
			if sums == "" {
				sum := sha256.Sum256(rs.binary)
				sums = hex.EncodeToString(sum[:]) + "  " + asset + "\n"
			}
			_, _ = w.Write([]byte(sums))
		case r.URL.Path == "/dl/"+asset:
			_, _ = w.Write(rs.binary)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(rs.Close)
	setVar(t, &githubAPIBase, rs.URL)
	return rs
}

// standaloneExe installs a fake standalone executable and makes executable() report it.
func standaloneExe(t *testing.T) string {
	t.Helper()
	exe := fakeBinary(t, t.TempDir(), "gcse-revision")
	withExecutable(t, exe, nil)
	return exe
}

// brewInstall makes executable() look like a Homebrew Cellar install with a working brew and app shim.
func brewInstall(t *testing.T) (brew, shim string) {
	t.Helper()
	root := t.TempDir()
	cellar := fakeBinary(t, filepath.Join(root, "Cellar", "gcse-revision", "1.0.0", "bin"), "gcse-revision")
	withExecutable(t, cellar, nil)
	brew = fakeBinary(t, root, "brew")
	shim = fakeBinary(t, filepath.Join(root, "bin"), "gcse-revision")
	setVar(t, &homebrewBinaries, []string{brew})
	setVar(t, &homebrewAppExecutables, []string{shim})
	return brew, shim
}

func scoopInstall(t *testing.T) {
	t.Helper()
	exe := fakeBinary(t, filepath.Join(t.TempDir(), "scoop", "apps", "gcse-revision", "current"), "gcse-revision.exe")
	withExecutable(t, exe, nil)
}

func TestVersionParsing(t *testing.T) {
	for _, bad := range []string{"dev", "v1.2", "v1.x.3", ""} {
		if _, err := versionParts(bad); err == nil {
			t.Fatalf("versionParts(%q) should fail", bad)
		}
	}
	if p, err := versionParts(" v1.2.3-rc.1 "); err != nil || p != [3]int{1, 2, 3} {
		t.Fatalf("pre-release suffix: %v %v", p, err)
	}
	if _, err := compareVersions("bad", "v1.0.0"); err == nil {
		t.Fatal("bad left version should fail")
	}
	if _, err := compareVersions("v1.0.0", "bad"); err == nil {
		t.Fatal("bad right version should fail")
	}
	if _, err := checksumFor([]byte("short  gcse-revision-linux-amd64\n"), "gcse-revision-linux-amd64"); err == nil {
		t.Fatal("a non-sha256 checksum must be rejected")
	}
	sum := strings.Repeat("a", 64)
	if got, err := checksumFor([]byte(sum+" *gcse-revision-linux-amd64\n"), "gcse-revision-linux-amd64"); err != nil || got != sum {
		t.Fatalf("binary-mode checksum line: %q %v", got, err)
	}
}

func TestUpgradeAssetNameAndQuoting(t *testing.T) {
	withOS(t, "windows", "arm64")
	if got := upgradeAssetName(); got != "gcse-revision-windows-arm64.exe" {
		t.Fatalf("windows asset = %q", got)
	}
	withOS(t, "linux", "amd64")
	if got := upgradeAssetName(); got != "gcse-revision-linux-amd64" {
		t.Fatalf("linux asset = %q", got)
	}
	if psQuote("it's") != "'it''s'" {
		t.Fatal("psQuote must double single quotes")
	}
}

func TestPackageManagedDetection(t *testing.T) {
	standaloneExe(t)
	if packageManaged() {
		t.Fatal("temp install should be standalone")
	}
	brewInstall(t)
	if !packageManaged() {
		t.Fatal("Cellar install should be package-managed")
	}
	withExecutable(t, "", errors.New("no exe"))
	if packageManaged() {
		t.Fatal("unknown executable must not be treated as package-managed")
	}
}

func TestLatestReleaseAndDownload(t *testing.T) {
	rs := newReleaseServer(t, "v9.9.9", []byte("binary"))
	rel, err := latestRelease(context.Background())
	if err != nil || rel.TagName != "v9.9.9" || len(rel.Assets) != 2 {
		t.Fatalf("latestRelease = %+v, %v", rel, err)
	}
	rs.tag = ""
	errContains(t, func() error { _, err := latestRelease(context.Background()); return err }(), "no version")
	rs.status = http.StatusForbidden
	errContains(t, func() error { _, err := latestRelease(context.Background()); return err }(), "403")

	bad := servePaths(t, map[string]string{"/repos/" + repo + "/releases/latest": "{not json"})
	setVar(t, &githubAPIBase, bad.URL)
	if _, err := latestRelease(context.Background()); err == nil {
		t.Fatal("invalid JSON should fail")
	}
	setVar(t, &githubAPIBase, "http://127.0.0.1:1")
	if _, err := latestRelease(context.Background()); err == nil {
		t.Fatal("unreachable API should fail")
	}
	setVar(t, &githubAPIBase, "http://bad host")
	if _, err := latestRelease(context.Background()); err == nil {
		t.Fatal("malformed URL should fail")
	}

	files := servePaths(t, map[string]string{"/ok": "12345"})
	if b, err := downloadBytes(context.Background(), files.URL+"/ok", 5); err != nil || string(b) != "12345" {
		t.Fatalf("download = %q, %v", b, err)
	}
	errContains(t, func() error { _, err := downloadBytes(context.Background(), files.URL+"/ok", 4); return err }(), "too large")
	errContains(t, func() error { _, err := downloadBytes(context.Background(), files.URL+"/missing", 4); return err }(), "404")
	if _, err := downloadBytes(context.Background(), "http://127.0.0.1:1/x", 4); err == nil {
		t.Fatal("unreachable download should fail")
	}
	if _, err := downloadBytes(context.Background(), "http://bad host/x", 4); err == nil {
		t.Fatal("malformed URL should fail")
	}
}

func TestSelfUpgradeStandalone(t *testing.T) {
	isolateHome(t)
	setVar(t, &version, "v1.0.0")
	exe := standaloneExe(t)
	rs := newReleaseServer(t, "v1.1.0", []byte("new binary"))

	updated, err := selfUpgrade(context.Background(), false)
	if err != nil || !updated {
		t.Fatalf("selfUpgrade = %v, %v", updated, err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new binary" {
		t.Fatalf("executable not replaced: %q", b)
	}

	rs.tag = "v1.0.0"
	if updated, err := selfUpgrade(context.Background(), false); err != nil || updated {
		t.Fatalf("same version should be a no-op: %v %v", updated, err)
	}
	rs.tag = "garbage"
	if _, err := selfUpgrade(context.Background(), false); err == nil {
		t.Fatal("unparseable release tag should fail")
	}
	rs.tag = "v2.0.0"
	rs.omit = "checksums.txt"
	errContains(t, func() error { _, err := selfUpgrade(context.Background(), false); return err }(), "missing")
	rs.omit = ""
	rs.sums = strings.Repeat("0", 64) + "  " + upgradeAssetName() + "\n"
	errContains(t, func() error { _, err := selfUpgrade(context.Background(), false); return err }(), "checksum mismatch")
	rs.sums = strings.Repeat("0", 64) + "  other-asset\n"
	errContains(t, func() error { _, err := selfUpgrade(context.Background(), false); return err }(), "no checksum")
	rs.sums = ""
	rs.assetErr = "checksums.txt"
	errContains(t, func() error { _, err := selfUpgrade(context.Background(), false); return err }(), "500")
	rs.assetErr = upgradeAssetName()
	errContains(t, func() error { _, err := selfUpgrade(context.Background(), false); return err }(), "500")
	rs.assetErr = ""
	rs.status = http.StatusBadGateway
	if _, err := selfUpgrade(context.Background(), false); err == nil {
		t.Fatal("release lookup failure should propagate")
	}

	withExecutable(t, "", errors.New("no exe"))
	if _, err := selfUpgrade(context.Background(), false); err == nil {
		t.Fatal("executable lookup failure should propagate")
	}
	withExecutable(t, filepath.Join(t.TempDir(), "missing"), nil)
	if _, err := selfUpgrade(context.Background(), false); err == nil {
		t.Fatal("unresolvable executable should fail")
	}
}

func TestInstallUpgrade(t *testing.T) {
	isolateHome(t)
	withOS(t, "linux", "amd64")
	exe := fakeBinary(t, t.TempDir(), "gcse-revision")
	var execed string
	setVar(t, &execSelfFn, func(path string, _, _ []string) error { execed = path; return nil })
	if err := installUpgrade(exe, []byte("v2"), true); err != nil {
		t.Fatal(err)
	}
	if execed != exe {
		t.Fatalf("restart should re-exec %s, got %q", exe, execed)
	}
	if err := installUpgrade(filepath.Join(t.TempDir(), "missing-dir", "exe"), []byte("v2"), false); err == nil {
		t.Fatal("unwritable staging path should fail")
	}
	// Renaming over a non-empty directory fails and must clean up the staged file.
	dirExe := filepath.Join(t.TempDir(), "exe")
	writeFile(t, filepath.Join(dirExe, "keep"), "x")
	if err := installUpgrade(dirExe, []byte("v2"), false); err == nil {
		t.Fatal("rename onto a directory should fail")
	}
	if _, err := os.Stat(dirExe + ".new"); !os.IsNotExist(err) {
		t.Fatal("staged binary left behind")
	}

	withOS(t, "windows", "amd64")
	fx := installFakeExec(t, func(string) fakeResult { return ok("") })
	if err := installUpgrade(exe, []byte("v3"), false); err != nil {
		t.Fatal(err)
	}
	if !fx.called("powershell.exe -NoProfile -ExecutionPolicy Bypass") {
		t.Fatalf("calls = %v", fx.Calls())
	}
	script, _ := os.ReadFile(filepath.Join(dataDir(), "apply-upgrade.ps1"))
	mustContain(t, string(script), "Move-Item -Force", psQuote(exe+".new"), "Remove-Item")
	if err := installUpgrade(exe, []byte("v3"), true); !errors.Is(err, errProcessExitForUpgrade) {
		t.Fatalf("windows restart should hand over to the script, got %v", err)
	}
	script, _ = os.ReadFile(filepath.Join(dataDir(), "apply-upgrade.ps1"))
	mustContain(t, string(script), "Start-Process -FilePath")
	if err := restartCurrentProcess(exe); err != nil {
		t.Fatal("windows restart is a no-op")
	}

	setVar(t, &execCommand, func(string, ...string) *exec.Cmd { return exec.Command(filepath.Join(t.TempDir(), "nope")) })
	if err := installUpgrade(exe, []byte("v3"), false); err == nil {
		t.Fatal("powershell start failure should propagate")
	}
	blockCacheDir(t)
	if err := installWindowsUpgrade(exe, exe+".new", false); err == nil {
		t.Fatal("unwritable script path should fail")
	}
}

func TestManagedUpgradeHomebrew(t *testing.T) {
	isolateHome(t)
	brewInstall(t)
	fx := installFakeExec(t, func(string) fakeResult { return ok("") })
	if updated, err := managedUpgrade(false); err != nil || updated {
		t.Fatalf("nothing outdated: %v %v", updated, err)
	}
	if !fx.called("brew update --quiet") || !fx.called("brew outdated --quiet gautampachnanda101/tap/gcse-revision") {
		t.Fatalf("calls = %v", fx.Calls())
	}

	fx = installFakeExec(t, func(c string) fakeResult {
		if strings.HasPrefix(c, "brew outdated") {
			return fail("gcse-revision")
		}
		return ok("")
	})
	if updated, err := managedUpgrade(false); err != nil || !updated || !fx.called("brew upgrade gautampachnanda101/tap/gcse-revision") {
		t.Fatalf("upgrade: %v %v %v", updated, err, fx.Calls())
	}
	fx = installFakeExec(t, func(c string) fakeResult {
		if strings.HasPrefix(c, "brew outdated") {
			return ok("gcse-revision")
		}
		return ok("")
	})
	if updated, err := managedUpgrade(true); !updated || !errors.Is(err, errProcessExitForUpgrade) || !fx.called("gcse-revision service install") {
		t.Fatalf("restart: %v %v %v", updated, err, fx.Calls())
	}
	installFakeExec(t, func(c string) fakeResult {
		switch {
		case strings.HasPrefix(c, "brew outdated"):
			return ok("gcse-revision")
		case strings.HasPrefix(c, "gcse-revision service install"):
			return fail("launchctl error")
		}
		return ok("")
	})
	errContains(t, func() error { _, err := managedUpgrade(true); return err }(), "install upgraded background service")
	installFakeExec(t, func(c string) fakeResult {
		if strings.HasPrefix(c, "brew upgrade") || strings.HasPrefix(c, "brew outdated") {
			return fail("Error: no bottle")
		}
		return ok("")
	})
	errContains(t, func() error { _, err := managedUpgrade(false); return err }(), "package manager upgrade")

	installFakeExec(t, func(c string) fakeResult {
		if strings.HasPrefix(c, "brew outdated") {
			return ok("gcse-revision")
		}
		return ok("")
	})
	setVar(t, &homebrewAppExecutables, []string{filepath.Join(t.TempDir(), "missing")})
	errContains(t, func() error { _, err := managedUpgrade(true); return err }(), "Homebrew executable is unavailable")

	setVar(t, &homebrewBinaries, []string{filepath.Join(t.TempDir(), "missing")})
	setVar(t, &lookPath, func(string) (string, error) { return "", errors.New("not found") })
	errContains(t, func() error { _, err := managedUpgrade(false); return err }(), "brew command is unavailable")
}

func TestManagedUpgradeScoop(t *testing.T) {
	isolateHome(t)
	scoopInstall(t)
	withOS(t, "linux", "amd64")
	installFakeExec(t, func(string) fakeResult { return ok("Everything is ok!") })
	if updated, err := managedUpgrade(false); err != nil || updated {
		t.Fatalf("no update: %v %v", updated, err)
	}
	fx := installFakeExec(t, func(c string) fakeResult {
		switch {
		case strings.HasPrefix(c, "scoop status"):
			return ok("Updates are available for: gcse-revision")
		case strings.HasPrefix(c, "scoop which"):
			return ok("C:/scoop/shims/gcse-revision.exe\n")
		}
		return ok("")
	})
	if updated, err := managedUpgrade(false); err != nil || !updated || !fx.called("scoop update gcse-revision") {
		t.Fatalf("scoop update: %v %v %v", updated, err, fx.Calls())
	}
	if _, err := managedUpgrade(true); !errors.Is(err, errProcessExitForUpgrade) {
		t.Fatalf("scoop restart: %v", err)
	}
	installFakeExec(t, func(c string) fakeResult {
		if strings.HasPrefix(c, "scoop which") {
			return fail("not found")
		}
		return ok("update")
	})
	errContains(t, func() error { _, err := managedUpgrade(true); return err }(), "locate Scoop executable")

	// On Windows the running exe is locked, so Scoop's update is staged in a detached script.
	withOS(t, "windows", "amd64")
	fx = installFakeExec(t, func(string) fakeResult { return ok("update available") })
	out := captureStdout(t, func() {
		if updated, err := managedUpgrade(false); err != nil || !updated {
			t.Fatalf("staged scoop: %v %v", updated, err)
		}
	})
	mustContain(t, out, "Scoop upgrade has been scheduled")
	script, _ := os.ReadFile(filepath.Join(dataDir(), "apply-scoop-upgrade.ps1"))
	mustContain(t, string(script), "scoop update gcse-revision", "service install", "Remove-Item")
	if _, err := managedUpgrade(true); !errors.Is(err, errProcessExitForUpgrade) {
		t.Fatalf("staged scoop restart: %v", err)
	}
	setVar(t, &execCommand, func(name string, args ...string) *exec.Cmd {
		if strings.HasPrefix(name, "powershell") {
			return exec.Command(filepath.Join(t.TempDir(), "nope"))
		}
		return fx.command(context.Background(), name, args)
	})
	if _, err := stageScoopUpgrade("x", false); err == nil {
		t.Fatal("powershell start failure should propagate")
	}
	blockCacheDir(t)
	if _, err := stageScoopUpgrade("x", false); err == nil {
		t.Fatal("unwritable script path should fail")
	}

	withExecutable(t, "/usr/local/opt/other/bin/x", nil)
	errContains(t, func() error { _, err := managedUpgrade(false); return err }(), "not supported")
}

func TestFindHomebrew(t *testing.T) {
	brew := fakeBinary(t, t.TempDir(), "brew")
	setVar(t, &homebrewBinaries, []string{filepath.Join(t.TempDir(), "missing"), brew})
	if got, err := findHomebrew(); err != nil || got != brew {
		t.Fatalf("candidate: %q %v", got, err)
	}
	setVar(t, &homebrewBinaries, nil)
	setVar(t, &lookPath, func(string) (string, error) { return "/somewhere/brew", nil })
	if got, err := findHomebrew(); err != nil || got != "/somewhere/brew" {
		t.Fatalf("PATH lookup: %q %v", got, err)
	}
	setVar(t, &lookPath, func(string) (string, error) { return "", errors.New("nope") })
	if _, err := findHomebrew(); err == nil {
		t.Fatal("expected an error without brew")
	}
}

func TestInstalledPackageExecutable(t *testing.T) {
	_, shim := brewInstall(t)
	if got, err := installedPackageExecutable(); err != nil || got != shim {
		t.Fatalf("brew shim: %q %v", got, err)
	}
	scoopInstall(t)
	installFakeExec(t, func(string) fakeResult { return ok(" C:/scoop/shims/gcse-revision.exe \n") })
	if got, err := installedPackageExecutable(); err != nil || got != "C:/scoop/shims/gcse-revision.exe" {
		t.Fatalf("scoop which: %q %v", got, err)
	}
	standaloneExe(t)
	errContains(t, func() error { _, err := installedPackageExecutable(); return err }(), "not package-managed")
	withExecutable(t, "", errors.New("no exe"))
	if _, err := installedPackageExecutable(); err == nil {
		t.Fatal("executable error should propagate")
	}
}

func TestReconcileManagedService(t *testing.T) {
	setVar(t, &version, "v1.0.0")
	standaloneExe(t)
	if repaired, err := reconcileManagedService(); repaired || err != nil {
		t.Fatalf("standalone: %v %v", repaired, err)
	}
	brewInstall(t)
	for _, tc := range []struct {
		name     string
		handler  func(string) fakeResult
		repaired bool
		errPart  string
	}{
		{"same version", func(string) fakeResult { return ok("gcse-revision v1.0.0\n") }, false, ""},
		{"no version", func(string) fakeResult { return ok("") }, false, ""},
		{"version fails", func(string) fakeResult { return fail("crash") }, false, "read installed package version"},
		{"new version", func(string) fakeResult { return ok("gcse-revision v1.1.0") }, true, ""},
		{"install fails", func(c string) fakeResult {
			if strings.Contains(c, "service install") {
				return fail("denied")
			}
			return ok("gcse-revision v1.1.0")
		}, false, "replace background service"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			installFakeExec(t, tc.handler)
			repaired, err := reconcileManagedService()
			if repaired != tc.repaired || (tc.errPart == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), tc.errPart)) {
				t.Fatalf("repaired=%v err=%v", repaired, err)
			}
		})
	}
	setVar(t, &homebrewAppExecutables, nil)
	if _, err := reconcileManagedService(); err == nil {
		t.Fatal("missing package executable should fail")
	}
}

func TestUpgradeCommand(t *testing.T) {
	isolateHome(t)
	fastService(t)
	withOS(t, "linux", "amd64")
	setVar(t, &version, "dev")
	errContains(t, upgradeCommand(context.Background()), "development builds")

	setVar(t, &version, "v1.0.0")
	standaloneExe(t)
	rs := newReleaseServer(t, "v1.0.0", []byte("same"))
	installFakeExec(t, func(string) fakeResult { return fakeResult{stdout: "inactive", code: 3} })
	out := captureStdout(t, func() {
		if err := upgradeCommand(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	mustContain(t, out, "already current")

	rs.tag = "v1.2.0"
	if err := upgradeCommand(context.Background()); err != nil {
		t.Fatalf("upgrade with stopped service: %v", err)
	}

	// Service running: it is restarted with the new binary; a restart failure is reported.
	rs.tag = "v1.3.0"
	installFakeExec(t, func(c string) fakeResult {
		if strings.Contains(c, " stop ") {
			return fail("denied")
		}
		return ok("active")
	})
	errContains(t, upgradeCommand(context.Background()), "stop service")

	rs.status = http.StatusInternalServerError
	if err := upgradeCommand(context.Background()); err == nil {
		t.Fatal("release lookup failure should propagate")
	}
}

func TestAutomaticUpgradeLoop(t *testing.T) {
	isolateHome(t)
	setVar(t, &upgradeFirstCheck, time.Millisecond)
	setVar(t, &upgradeInterval, time.Hour)
	setVar(t, &managedUpgradePoll, time.Hour)

	// Disabled cases return without contacting anything.
	rs := newReleaseServer(t, "v1.0.0", []byte("x"))
	setVar(t, &version, "dev")
	automaticUpgradeLoop(context.Background(), options{})
	setVar(t, &version, "v1.0.0")
	automaticUpgradeLoop(context.Background(), options{offline: true})
	t.Setenv("GCSE_REVISION_AUTO_UPDATE", "0")
	automaticUpgradeLoop(context.Background(), options{})
	t.Setenv("GCSE_REVISION_AUTO_UPDATE", "")
	time.Sleep(20 * time.Millisecond)
	if rs.hits.Load() != 0 {
		t.Fatal("disabled auto-upgrade contacted the release API")
	}

	waitFor := func(cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatal("timed out")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	// Standalone, already current: checks once, then waits for the next interval until cancelled.
	standaloneExe(t)
	ctx, cancel := context.WithCancel(context.Background())
	automaticUpgradeLoop(ctx, options{})
	waitFor(func() bool { return rs.hits.Load() == 1 })
	cancel()

	// Lookup failure is logged and retried later.
	rs.status = http.StatusInternalServerError
	ctx, cancel = context.WithCancel(context.Background())
	automaticUpgradeLoop(ctx, options{})
	waitFor(func() bool { return rs.hits.Load() == 2 })
	cancel()
	rs.status = 0

	// An update is installed and the loop stops.
	rs.tag = "v1.1.0"
	var execed atomic.Bool
	setVar(t, &execSelfFn, func(string, []string, []string) error { execed.Store(true); return nil })
	automaticUpgradeLoop(context.Background(), options{})
	waitFor(execed.Load)

	// Package-managed: reconciliation repairs the service and ends the loop.
	brewInstall(t)
	var reconciled atomic.Bool
	installFakeExec(t, func(c string) fakeResult {
		if strings.Contains(c, "service install") {
			reconciled.Store(true)
		}
		return ok("gcse-revision v9.0.0")
	})
	automaticUpgradeLoop(context.Background(), options{})
	waitFor(reconciled.Load)

	// Package-managed: reconciliation fails, then the package-manager upgrade asks the process to exit.
	var exited atomic.Bool
	setVar(t, &exitProcess, func(int) { exited.Store(true) })
	setVar(t, &version, "v9.0.0")
	installFakeExec(t, func(c string) fakeResult {
		switch {
		case strings.HasPrefix(c, "gcse-revision version"):
			return fail("broken")
		case strings.HasPrefix(c, "brew outdated"):
			return ok("gcse-revision")
		}
		return ok("")
	})
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	automaticUpgradeLoop(ctx, options{})
	waitFor(exited.Load)
}
