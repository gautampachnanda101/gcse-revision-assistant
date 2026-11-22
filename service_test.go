package main

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// serviceEnv prepares an isolated home, a real executable path and fast timings.
func serviceEnv(t *testing.T, goos string) (home, exe string) {
	t.Helper()
	home = isolateHome(t)
	withOS(t, goos, "amd64")
	fastService(t)
	exe = fakeBinary(t, t.TempDir(), "gcse-revision")
	withExecutable(t, exe, nil)
	setVar(t, &homebrewBinaries, []string{filepath.Join(home, "no-brew")})
	setVar(t, &lookPath, func(string) (string, error) { return "", errors.New("not found") })
	return home, exe
}

func plistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", serviceLabel()+".plist")
}

func TestServiceStatusPerPlatform(t *testing.T) {
	for _, tc := range []struct {
		goos    string
		handler func(string) fakeResult
		running bool
	}{
		{"darwin", func(c string) fakeResult {
			if strings.HasPrefix(c, "launchctl list sh.brew") {
				return ok(`{"PID" = 42;}`)
			}
			return fail("Could not find service")
		}, true},
		{"darwin", func(string) fakeResult { return fail("Could not find service") }, false},
		{"linux", func(string) fakeResult { return ok("active\n") }, true},
		{"linux", func(string) fakeResult { return fakeResult{stdout: "inactive", code: 3} }, false},
		{"windows", func(string) fakeResult { return ok("Status: Running") }, true},
		{"windows", func(string) fakeResult { return ok("Status: Ready") }, false},
	} {
		t.Run(tc.goos+"/"+strconv.FormatBool(tc.running), func(t *testing.T) {
			serviceEnv(t, tc.goos)
			installFakeExec(t, tc.handler)
			running, _, _ := serviceStatus()
			if running != tc.running {
				t.Fatalf("running = %v, want %v", running, tc.running)
			}
		})
	}
	serviceEnv(t, "plan9")
	if _, _, err := serviceStatus(); err == nil {
		t.Fatal("unsupported OS should error")
	}
}

func TestServiceNamesAndQuoting(t *testing.T) {
	if serviceUnit() != "gcse-revision.service" || len(serviceLabels()) != 3 || serviceLabels()[1] != "sh.brew.gcse-revision" {
		t.Fatal("service identifiers changed; AGENTS.md §6 depends on them")
	}
	if got := xmlEscape(`a&b<c>"d'`); got != "a&amp;b&lt;c&gt;&quot;d&apos;" {
		t.Fatalf("xmlEscape = %q", got)
	}
	if got := shellQuote("it's"); got != `'it'"'"'s'` {
		t.Fatalf("shellQuote = %q", got)
	}
	for _, s := range []string{"Could not find service", "service not loaded", "ERROR: not found", "No such process"} {
		if !serviceNotFound(s) {
			t.Fatalf("serviceNotFound(%q) = false", s)
		}
	}
	if serviceNotFound("permission denied") {
		t.Fatal("unrelated error treated as not found")
	}
	if ignoreMissing(os.ErrNotExist) != nil || ignoreMissing(nil) != nil || ignoreMissing(os.ErrPermission) == nil {
		t.Fatal("ignoreMissing mishandles errors")
	}
}

func TestCurrentUserIDAndHome(t *testing.T) {
	home := isolateHome(t)
	if currentUserID() != "501" {
		t.Fatalf("uid = %q", currentUserID())
	}
	if h, err := realUserHomeDir(); err != nil || h != home {
		t.Fatalf("home = %q, %v", h, err)
	}
	setVar(t, &currentUser, func() (*user.User, error) { return nil, errors.New("no user db") })
	if currentUserID() != "0" {
		t.Fatal("uid fallback should be 0")
	}
	if h, err := realUserHomeDir(); err != nil || h != home {
		t.Fatalf("home fallback = %q, %v (want $HOME)", h, err)
	}
}

func TestInstallServiceDarwinWritesLaunchAgent(t *testing.T) {
	home, exe := serviceEnv(t, "darwin")
	port := unusedPort(t)
	fx := installFakeExec(t, func(c string) fakeResult {
		switch {
		case strings.HasPrefix(c, "launchctl bootstrap"):
			appOnPort(t, port)
			return ok("")
		case strings.HasPrefix(c, "launchctl list"):
			return fail("Could not find service")
		}
		return ok("")
	})
	out := captureStdout(t, func() {
		if err := installService(options{port: port}); err != nil {
			t.Fatal(err)
		}
	})
	mustContain(t, out, "installed")
	b, err := os.ReadFile(plistPath(home))
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(exe)
	mustContain(t, string(b), serviceLabel(), xmlEscape(resolved), "<string>--service</string>", strconv.Itoa(port), "<string>tester</string>")
	if !fx.called("launchctl bootout gui/501/"+serviceLabel()) || !fx.called("launchctl bootstrap gui/501") {
		t.Fatalf("launchctl calls = %v", fx.Calls())
	}
}

func TestInstallServiceDarwinDelegatesToHomebrew(t *testing.T) {
	home, _ := serviceEnv(t, "darwin")
	writeFile(t, filepath.Join(home, "Library", "LaunchAgents", "sh.brew.gcse-revision.plist"), "<plist/>")
	brew := fakeBinary(t, t.TempDir(), "brew")
	setVar(t, &homebrewBinaries, []string{brew})
	port := unusedPort(t)
	fx := installFakeExec(t, func(c string) fakeResult {
		switch {
		case strings.HasPrefix(c, "brew services start"):
			appOnPort(t, port)
		case strings.HasPrefix(c, "launchctl list"):
			return fail("Could not find service")
		}
		return ok("")
	})
	if err := installService(options{port: port}); err != nil {
		t.Fatal(err)
	}
	if !fx.called("brew services stop gautampachnanda101/tap/gcse-revision") || !fx.called("brew services start") {
		t.Fatalf("Homebrew-managed install must delegate to brew services, calls = %v", fx.Calls())
	}
	if _, err := os.Stat(plistPath(home)); !os.IsNotExist(err) {
		t.Fatal("must not write a competing LaunchAgent for a Homebrew-managed service")
	}
}

func TestInstallServiceDarwinPackageDelegatesWithoutHomebrewPlist(t *testing.T) {
	home, _ := serviceEnv(t, "darwin")
	brew := fakeBinary(t, t.TempDir(), "brew")
	exe := fakeBinary(t, filepath.Join(home, "Cellar", "gcse-revision", "0.7.16", "bin"), "gcse-revision")
	withExecutable(t, exe, nil)
	setVar(t, &homebrewBinaries, []string{brew})
	port := unusedPort(t)
	legacy := plistPath(home)
	writeFile(t, legacy, "<plist/>")
	fx := installFakeExec(t, func(c string) fakeResult {
		switch {
		case strings.HasPrefix(c, "brew services start"):
			appOnPort(t, port)
		case strings.HasPrefix(c, "launchctl list"):
			return fail("Could not find service")
		}
		return ok("")
	})

	if err := installService(options{port: port}); err != nil {
		t.Fatal(err)
	}
	if !fx.called("brew services stop gautampachnanda101/tap/gcse-revision") ||
		!fx.called("launchctl bootout gui/501/"+serviceLabel()) ||
		!fx.called("brew services start gautampachnanda101/tap/gcse-revision") {
		t.Fatalf("package-managed install did not migrate to Homebrew service: %v", fx.Calls())
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy plist should be removed during Homebrew migration: %v", err)
	}
}

func TestStopServiceHomebrewMigrationFailures(t *testing.T) {
	setup := func(t *testing.T) string {
		home, _ := serviceEnv(t, "darwin")
		exe := fakeBinary(t, filepath.Join(home, "Cellar", "gcse-revision", "0.7.16", "bin"), "gcse-revision")
		withExecutable(t, exe, nil)
		brew := fakeBinary(t, t.TempDir(), "brew")
		setVar(t, &homebrewBinaries, []string{brew})
		return home
	}

	t.Run("legacy stop", func(t *testing.T) {
		setup(t)
		installFakeExec(t, func(c string) fakeResult {
			if strings.HasPrefix(c, "launchctl bootout") {
				return fail("permission denied")
			}
			return ok("")
		})
		errContains(t, stopService(), "stop legacy launch agent")
	})

	t.Run("home lookup", func(t *testing.T) {
		setup(t)
		setVar(t, &currentUser, func() (*user.User, error) { return nil, errors.New("no user db") })
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "")
		installFakeExec(t, func(c string) fakeResult {
			if strings.HasPrefix(c, "launchctl bootout") {
				return fail("Could not find service")
			}
			return ok("")
		})
		if err := stopService(); err == nil {
			t.Fatal("missing user home should fail")
		}
	})

	t.Run("remove plist", func(t *testing.T) {
		home := setup(t)
		legacy := plistPath(home)
		if err := os.MkdirAll(filepath.Join(legacy, "child"), 0o755); err != nil {
			t.Fatal(err)
		}
		installFakeExec(t, func(c string) fakeResult {
			if strings.HasPrefix(c, "launchctl bootout") {
				return fail("Could not find service")
			}
			return ok("")
		})
		errContains(t, stopService(), "remove legacy launch agent")
	})
}

func TestInstallServiceDarwinFailures(t *testing.T) {
	home, _ := serviceEnv(t, "darwin")
	installFakeExec(t, func(c string) fakeResult {
		if strings.HasPrefix(c, "launchctl list") {
			return ok("running") // never stops
		}
		return ok("")
	})
	errContains(t, installService(options{port: unusedPort(t)}), "start installed launch agent")

	func() {
		orig := currentUser
		defer func() { currentUser = orig }()
		currentUser = func() (*user.User, error) { return nil, errors.New("no user") }
		errContains(t, installService(options{port: 1}), "no user")
	}()

	// LaunchAgents can't be created because a file is in the way.
	if err := os.RemoveAll(filepath.Join(home, "Library")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, "Library"), "not a dir")
	if err := installService(options{port: unusedPort(t)}); err == nil {
		t.Fatal("expected a plist write error")
	}
}

func TestInstallServiceLinux(t *testing.T) {
	home, exe := serviceEnv(t, "linux")
	fx := installFakeExec(t, func(string) fakeResult { return ok("") })
	captureStdout(t, func() {
		if err := installService(options{port: 9001}); err != nil {
			t.Fatal(err)
		}
	})
	b, err := os.ReadFile(filepath.Join(home, ".config", "systemd", "user", serviceUnit()))
	if err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(exe)
	mustContain(t, string(b), "ExecStart="+shellQuote(resolved)+" serve --service --no-browser --no-ai --port 9001", "Restart=always", "WantedBy=default.target")
	if !fx.called("systemctl --user daemon-reload") || !fx.called("systemctl --user enable --now gcse-revision.service") {
		t.Fatalf("calls = %v", fx.Calls())
	}

	for _, failing := range []string{"systemctl --user daemon-reload", "systemctl --user enable"} {
		installFakeExec(t, func(c string) fakeResult {
			if strings.HasPrefix(c, failing) {
				return fail("boom")
			}
			return ok("")
		})
		errContains(t, installService(options{port: 9001}), "systemctl")
	}

	if err := os.RemoveAll(filepath.Join(home, ".config", "systemd")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".config", "systemd"), "file in the way")
	if err := installService(options{port: 9001}); err == nil {
		t.Fatal("expected a unit write error")
	}
}

func TestInstallServiceWindows(t *testing.T) {
	serviceEnv(t, "windows")
	fx := installFakeExec(t, func(string) fakeResult { return ok("SUCCESS") })
	captureStdout(t, func() {
		if err := installService(options{port: 8765}); err != nil {
			t.Fatal(err)
		}
	})
	if !fx.called("schtasks /End /TN GCSERevision") || !fx.called("schtasks /Create /TN GCSERevision /SC ONLOGON /RL LIMITED /F /TR") || !fx.called("schtasks /Run /TN GCSERevision") {
		t.Fatalf("calls = %v", fx.Calls())
	}
	for _, failing := range []string{"schtasks /Create", "schtasks /Run"} {
		installFakeExec(t, func(c string) fakeResult {
			if strings.HasPrefix(c, failing) {
				return fail("ERROR: Access is denied.")
			}
			return ok("")
		})
		errContains(t, installService(options{port: 8765}), "schtasks")
	}
}

func TestInstallServiceGeneralFailures(t *testing.T) {
	serviceEnv(t, "plan9")
	installFakeExec(t, func(string) fakeResult { return ok("") })
	errContains(t, installService(options{port: 1}), "not supported")

	withExecutable(t, "", errors.New("no exe"))
	errContains(t, installService(options{port: 1}), "no exe")

	withExecutable(t, filepath.Join(t.TempDir(), "missing"), nil)
	if err := installService(options{port: 1}); err == nil {
		t.Fatal("unresolvable executable should fail")
	}
}

func TestServiceConfigured(t *testing.T) {
	home, _ := serviceEnv(t, "darwin")
	if got, err := serviceConfigured(); got || err != nil {
		t.Fatalf("darwin unconfigured = %v, %v", got, err)
	}
	writeFile(t, plistPath(home), "<plist/>")
	if got, _ := serviceConfigured(); !got {
		t.Fatal("darwin plist not detected")
	}
	_ = os.Remove(plistPath(home))
	exe := fakeBinary(t, filepath.Join(home, "Cellar", "gcse-revision", "0.7.16", "bin"), "gcse-revision")
	withExecutable(t, exe, nil)
	if got, err := serviceConfigured(); !got || err != nil {
		t.Fatalf("darwin package-managed service = %v, %v", got, err)
	}

	withOS(t, "linux", "amd64")
	if got, _ := serviceConfigured(); got {
		t.Fatal("linux unit should be absent")
	}
	writeFile(t, filepath.Join(home, ".config", "systemd", "user", serviceUnit()), "[Unit]")
	if got, _ := serviceConfigured(); !got {
		t.Fatal("linux unit not detected")
	}

	withOS(t, "windows", "amd64")
	installFakeExec(t, func(string) fakeResult { return ok("") })
	if got, _ := serviceConfigured(); !got {
		t.Fatal("windows task not detected")
	}
	installFakeExec(t, func(string) fakeResult { return fail("ERROR: not found") })
	if got, _ := serviceConfigured(); got {
		t.Fatal("windows task should be absent")
	}

	withOS(t, "plan9", "amd64")
	if _, err := serviceConfigured(); err == nil {
		t.Fatal("unsupported OS should error")
	}
}

func TestStartService(t *testing.T) {
	home, _ := serviceEnv(t, "darwin")
	fx := installFakeExec(t, func(string) fakeResult { return ok("") })
	if err := startService(); err != nil || !fx.called("launchctl bootstrap gui/501 "+plistPath(home)) {
		t.Fatalf("bootstrap: %v %v", err, fx.Calls())
	}
	fx = installFakeExec(t, func(c string) fakeResult {
		if strings.HasPrefix(c, "launchctl bootstrap") {
			return fail("Bootstrap failed: 5")
		}
		return ok("")
	})
	if err := startService(); err != nil || !fx.called("launchctl load -w") {
		t.Fatalf("legacy load fallback: %v %v", err, fx.Calls())
	}
	installFakeExec(t, func(string) fakeResult { return fail("nope") })
	errContains(t, startService(), "bootstrap")

	// Homebrew-owned service.
	writeFile(t, filepath.Join(home, "Library", "LaunchAgents", "sh.brew.gcse-revision.plist"), "<plist/>")
	errContains(t, startService(), "brew command is unavailable")
	brew := fakeBinary(t, t.TempDir(), "brew")
	setVar(t, &homebrewBinaries, []string{brew})
	fx = installFakeExec(t, func(string) fakeResult { return ok("") })
	if err := startService(); err != nil || !fx.called("brew services start gautampachnanda101/tap/gcse-revision") {
		t.Fatalf("brew start: %v %v", err, fx.Calls())
	}
	installFakeExec(t, func(string) fakeResult { return fail("Error: permission") })
	errContains(t, startService(), "start Homebrew service")

	for goos, want := range map[string]string{"linux": "systemctl --user start gcse-revision.service", "windows": "schtasks /Run /TN GCSERevision"} {
		withOS(t, goos, "amd64")
		fx = installFakeExec(t, func(string) fakeResult { return ok("") })
		if err := startService(); err != nil || !fx.called(want) {
			t.Fatalf("%s start: %v %v", goos, err, fx.Calls())
		}
		installFakeExec(t, func(string) fakeResult { return fail("denied") })
		errContains(t, startService(), "start service")
	}
	withOS(t, "plan9", "amd64")
	errContains(t, startService(), "not supported")
}

func TestStopService(t *testing.T) {
	home, _ := serviceEnv(t, "darwin")
	fx := installFakeExec(t, func(string) fakeResult { return ok("") })
	if err := stopService(); err != nil || !fx.called("launchctl bootout gui/501/"+serviceLabel()) {
		t.Fatalf("bootout: %v %v", err, fx.Calls())
	}
	installFakeExec(t, func(string) fakeResult { return fail("Boot-out failed: 3: No such process") })
	if err := stopService(); err != nil {
		t.Fatalf("stopping a missing service should succeed: %v", err)
	}
	installFakeExec(t, func(string) fakeResult { return fail("Operation not permitted") })
	errContains(t, stopService(), "stop service")

	writeFile(t, filepath.Join(home, "Library", "LaunchAgents", "sh.brew.gcse-revision.plist"), "<plist/>")
	errContains(t, stopService(), "brew command is unavailable")
	brew := fakeBinary(t, t.TempDir(), "brew")
	setVar(t, &homebrewBinaries, []string{brew})
	fx = installFakeExec(t, func(string) fakeResult { return ok("") })
	if err := stopService(); err != nil || !fx.called("brew services stop") {
		t.Fatalf("brew stop: %v %v", err, fx.Calls())
	}
	installFakeExec(t, func(string) fakeResult { return fail("Error: Service `gcse-revision` is not loaded") })
	if err := stopService(); err != nil {
		t.Fatalf("brew stop of an unloaded service should succeed: %v", err)
	}
	installFakeExec(t, func(string) fakeResult { return fail("Error: permission denied") })
	errContains(t, stopService(), "stop Homebrew service")

	for goos, want := range map[string]string{"linux": "systemctl --user stop", "windows": "schtasks /End /TN GCSERevision"} {
		withOS(t, goos, "amd64")
		fx = installFakeExec(t, func(string) fakeResult { return ok("") })
		if err := stopService(); err != nil || !fx.called(want) {
			t.Fatalf("%s stop: %v %v", goos, err, fx.Calls())
		}
	}
	withOS(t, "plan9", "amd64")
	errContains(t, stopService(), "not supported")
}

func TestRestartServiceAt(t *testing.T) {
	serviceEnv(t, "linux")
	stopFails := installFakeExec(t, func(c string) fakeResult {
		if strings.Contains(c, " stop ") {
			return fail("denied")
		}
		return ok("")
	})
	errContains(t, restartServiceAt(unusedPort(t)), "stop service")
	_ = stopFails

	// Status keeps reporting active, so the stop never completes.
	installFakeExec(t, func(c string) fakeResult { return ok("active") })
	errContains(t, restartServiceAt(unusedPort(t)), "did not stop in time")

	// Start keeps failing.
	installFakeExec(t, func(c string) fakeResult {
		switch {
		case strings.Contains(c, "is-active"):
			return fakeResult{stdout: "inactive", code: 3}
		case strings.Contains(c, " start "):
			return fail("start failed")
		}
		return ok("")
	})
	errContains(t, restartServiceAt(unusedPort(t)), "did not start after upgrade")

	// Start succeeds but nothing answers on the port.
	installFakeExec(t, func(c string) fakeResult {
		if strings.Contains(c, "is-active") {
			return fakeResult{stdout: "inactive", code: 3}
		}
		return ok("")
	})
	errContains(t, restartServiceAt(unusedPort(t)), "is not responding")

	// restartService targets the default port; whatever it returns, it must not hang.
	_ = restartService()
}

func TestUninstallService(t *testing.T) {
	home, _ := serviceEnv(t, "darwin")
	writeFile(t, plistPath(home), "<plist/>")
	installFakeExec(t, func(string) fakeResult { return ok("") })
	if err := uninstallService(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plistPath(home)); !os.IsNotExist(err) {
		t.Fatal("plist not removed")
	}
	setVar(t, &currentUser, func() (*user.User, error) { return nil, errors.New("x") })
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if err := uninstallService(); err == nil {
		t.Fatal("expected a home lookup failure")
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	withOS(t, "linux", "amd64")
	unit := filepath.Join(home, ".config", "systemd", "user", serviceUnit())
	writeFile(t, unit, "[Unit]")
	fx := installFakeExec(t, func(string) fakeResult { return ok("") })
	if err := uninstallService(); err != nil || !fx.called("systemctl --user disable") || !fx.called("systemctl --user daemon-reload") {
		t.Fatalf("linux uninstall: %v %v", err, fx.Calls())
	}
	if _, err := os.Stat(unit); !os.IsNotExist(err) {
		t.Fatal("unit not removed")
	}
	installFakeExec(t, func(string) fakeResult { return fail("denied") })
	errContains(t, uninstallService(), "systemctl disable")

	withOS(t, "windows", "amd64")
	installFakeExec(t, func(string) fakeResult { return fail("ERROR: The system cannot find the file specified. not found") })
	if err := uninstallService(); err != nil {
		t.Fatalf("deleting a missing task should succeed: %v", err)
	}
	installFakeExec(t, func(string) fakeResult { return fail("Access is denied") })
	errContains(t, uninstallService(), "schtasks delete")

	withOS(t, "plan9", "amd64")
	errContains(t, uninstallService(), "not supported")
}

func TestServiceCommandDispatch(t *testing.T) {
	home, _ := serviceEnv(t, "linux")
	fx := installFakeExec(t, func(c string) fakeResult {
		if strings.Contains(c, "is-active") {
			return ok("active")
		}
		return ok("")
	})
	errContains(t, serviceCommand(nil, options{}), "usage")
	errContains(t, serviceCommand([]string{"bogus"}, options{}), "unknown service command")

	out := captureStdout(t, func() {
		if err := serviceCommand([]string{"status"}, options{port: 8765}); err != nil {
			t.Fatal(err)
		}
	})
	mustContain(t, out, "running", "gcse-revision.localhost:8765")

	out = captureStdout(t, func() {
		if err := serviceCommand([]string{"ensure"}, options{port: 8765}); err != nil {
			t.Fatal(err)
		}
	})
	mustContain(t, out, "not configured")

	writeFile(t, filepath.Join(home, ".config", "systemd", "user", serviceUnit()), "[Unit]")
	captureStdout(t, func() {
		if err := serviceCommand([]string{"ensure"}, options{port: 8765}); err != nil {
			t.Fatal(err)
		}
	})
	if !fx.called("systemctl --user enable --now") {
		t.Fatal("ensure should reinstall a configured service")
	}
	for _, sub := range []string{"start", "stop", "uninstall"} {
		if err := serviceCommand([]string{sub}, options{}); err != nil {
			t.Fatalf("%s: %v", sub, err)
		}
	}
	captureStdout(t, func() { _ = serviceCommand([]string{"install"}, options{port: 8765}) })
	_ = serviceCommand([]string{"restart"}, options{port: unusedPort(t)})

	installFakeExec(t, func(string) fakeResult { return fakeResult{stdout: "inactive", code: 3} })
	out = captureStdout(t, func() { _ = serviceCommand([]string{"status"}, options{port: 8765}) })
	mustContain(t, out, "stopped")

	errContains(t, serviceCommand([]string{"logs"}, options{}), "not available yet")

	withOS(t, "plan9", "amd64")
	if err := serviceCommand([]string{"ensure"}, options{}); err == nil {
		t.Fatal("ensure on an unsupported OS should fail")
	}
}

func TestServiceLogging(t *testing.T) {
	isolateHome(t)
	errContains(t, showServiceLogs(), "not available yet")
	if err := enableServiceLogging(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 150; i++ {
		logf("line %d", i)
	}
	closeServiceLogging()
	closeServiceLogging() // idempotent
	out := captureStdout(t, func() {
		if err := showServiceLogs(); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "line 49\n") || !strings.Contains(out, "line 50") || !strings.Contains(out, "line 149") {
		t.Fatalf("logs should show only the last 100 lines:\n%s", out)
	}

	// Log path is a directory → read error.
	if err := os.Remove(serviceLogPath()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(serviceLogPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := showServiceLogs(); err == nil || strings.Contains(err.Error(), "not available") {
		t.Fatalf("expected a read error, got %v", err)
	}
	if err := enableServiceLogging(); err == nil {
		t.Fatal("opening a directory as the log should fail")
	}
	// serviceDir is blocked by a file.
	if err := os.RemoveAll(serviceDir()); err != nil {
		t.Fatal(err)
	}
	writeFile(t, serviceDir(), "file")
	if err := enableServiceLogging(); err == nil {
		t.Fatal("expected a mkdir error")
	}
}
