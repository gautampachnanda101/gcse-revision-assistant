package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxUpgradeBytes = 128 << 20

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func automaticUpgradeLoop(ctx context.Context, opts options) {
	if _, err := versionParts(version); err != nil {
		return
	}
	if opts.offline || envOr("GCSE_REVISION_AUTO_UPDATE", "1") != "1" {
		return
	}
	go func() {
		interval := upgradeInterval
		if packageManaged() {
			interval = managedUpgradePoll
		}
		timer := time.NewTimer(upgradeFirstCheck)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if repaired, repairErr := reconcileManagedService(); repairErr != nil {
					logf("Packaged service reconciliation skipped (%v).", repairErr)
				} else if repaired {
					exitProcess(0)
					return
				}
				updated, err := selfUpgrade(ctx, true)
				if errors.Is(err, errProcessExitForUpgrade) {
					exitProcess(0)
				}
				if err != nil {
					logf("Automatic upgrade skipped (%v).", err)
				} else if updated {
					return
				}
				timer.Reset(interval)
			}

		}
	}()
}

func reconcileManagedService() (bool, error) {
	exePath, err := executable()
	if err != nil {
		return false, err
	}
	if !packageManagedPath(exePath) {
		return false, nil
	}
	current, err := installedPackageExecutable()
	if err != nil {
		return false, err
	}
	out, err := execCommand(current, "version").CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("read installed package version: %s: %w", strings.TrimSpace(string(out)), err)
	}
	fields := strings.Fields(string(out))
	installedVersion := ""
	if len(fields) >= 2 {
		installedVersion = fields[len(fields)-1]
	}
	if installedVersion == "" || installedVersion == version {
		return false, nil
	}
	lower := strings.ToLower(strings.ReplaceAll(filepath.ToSlash(exePath), `\`, "/"))
	if strings.Contains(lower, "/cellar/") || strings.Contains(lower, "/homebrew/") {
		if err := startHomebrewPackageService(); err != nil {
			return false, err
		}
		return true, nil
	}
	cmd := execCommand(current, "service", "install")
	if out, err = cmd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("replace background service: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return true, nil
}

func installedPackageExecutable() (string, error) {
	exe, err := executable()
	if err != nil {
		return "", err
	}
	lower := strings.ToLower(strings.ReplaceAll(filepath.ToSlash(exe), `\`, "/"))
	switch {
	case strings.Contains(lower, "/cellar/") || strings.Contains(lower, "/homebrew/"):
		for _, path := range homebrewAppExecutables {
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
		return "", errors.New("the active Homebrew executable is unavailable")
	case strings.Contains(lower, "/scoop/apps/"):
		out, err := execCommand("scoop", "which", appName).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("locate Scoop executable: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return strings.TrimSpace(string(out)), nil
	default:
		return "", errors.New("installation is not package-managed")
	}
}

func upgradeCommand(ctx context.Context) error {
	if version == "dev" {
		return errors.New("automatic upgrades are unavailable in development builds")
	}
	updated, err := selfUpgrade(ctx, false)
	if err != nil {
		return err
	}
	if !updated {
		logf("GCSE Revision %s is already current.", version)
		return nil
	}
	if running, _, _ := serviceStatus(); running {
		logf("Restarting the background service with the upgraded version…")
		if err := restartService(); err != nil {
			return err
		}
	}
	return nil
}

func selfUpgrade(ctx context.Context, restart bool) (bool, error) {
	exe, err := executable()
	if err != nil {
		return false, err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return false, err
	}
	if packageManagedPath(exe) {
		return managedUpgrade(restart)
	}
	rel, err := latestRelease(ctx)
	if err != nil {
		return false, err
	}
	cmp, err := compareVersions(rel.TagName, version)
	if err != nil {
		return false, err
	}
	if cmp <= 0 {
		return false, nil
	}
	asset := upgradeAssetName()
	var binaryURL, checksumURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case asset:
			binaryURL = a.BrowserDownloadURL
		case "checksums.txt":
			checksumURL = a.BrowserDownloadURL
		}
	}
	if binaryURL == "" || checksumURL == "" {
		return false, fmt.Errorf("release %s is missing %s or checksums.txt", rel.TagName, asset)
	}
	checksums, err := downloadBytes(ctx, checksumURL, 1<<20)
	if err != nil {
		return false, err
	}
	want, err := checksumFor(checksums, asset)
	if err != nil {
		return false, err
	}
	binary, err := downloadBytes(ctx, binaryURL, maxUpgradeBytes)
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(binary)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(want, got) {
		return false, fmt.Errorf("checksum mismatch for %s", asset)
	}
	if err := installUpgrade(exe, binary, restart); err != nil {
		return false, err
	}
	logf("Upgraded GCSE Revision from %s to %s.", version, rel.TagName)
	return true, nil
}

func latestRelease(ctx context.Context) (githubRelease, error) {
	url := strings.TrimRight(githubAPIBase, "/") + "/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", appName+"/"+version)
	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return githubRelease{}, fmt.Errorf("release lookup: %s", res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return githubRelease{}, err
	}
	var rel githubRelease
	if err := json.Unmarshal(b, &rel); err != nil {
		return githubRelease{}, err
	}
	if rel.TagName == "" {
		return githubRelease{}, errors.New("release lookup returned no version")
	}
	return rel, nil
}

func downloadBytes(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", appName+"/"+version)
	client := &http.Client{Timeout: 5 * time.Minute}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", filepath.Base(url), res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("download is too large")
	}
	return b, nil
}

func checksumFor(checksums []byte, name string) (string, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.TrimPrefix(fields[len(fields)-1], "*") == name {
			if len(fields[0]) == 64 {
				return fields[0], nil
			}
		}
	}
	return "", fmt.Errorf("no checksum found for %s", name)
}

func compareVersions(a, b string) (int, error) {
	ap, err := versionParts(a)
	if err != nil {
		return 0, err
	}
	bp, err := versionParts(b)
	if err != nil {
		return 0, err
	}
	for i := range ap {
		if ap[i] < bp[i] {
			return -1, nil
		}
		if ap[i] > bp[i] {
			return 1, nil
		}
	}
	return 0, nil
}

func versionParts(s string) ([3]int, error) {
	var out [3]int
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s = strings.SplitN(s, "-", 2)[0]
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return out, fmt.Errorf("invalid release version %q", s)
	}
	for i := range parts {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return out, fmt.Errorf("invalid release version %q", s)
		}
		out[i] = n
	}
	return out, nil
}

func upgradeAssetName() string {
	ext := ""
	osName := hostOS
	arch := hostArch
	if osName == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("%s-%s-%s%s", appName, osName, arch, ext)
}

func packageManaged() bool {
	exe, err := executable()
	return err == nil && packageManagedPath(exe)
}

func packageManagedPath(exe string) bool {
	lower := strings.ToLower(strings.ReplaceAll(filepath.ToSlash(exe), `\`, "/"))
	return strings.Contains(lower, "/cellar/") ||
		strings.Contains(lower, "/homebrew/") ||
		strings.Contains(lower, "/scoop/apps/")
}

func installUpgrade(exe string, b []byte, restart bool) error {
	next := exe + ".new"
	if err := os.WriteFile(next, b, 0o755); err != nil {
		return err
	}
	if hostOS == "windows" {
		return installWindowsUpgrade(exe, next, restart)
	}
	if err := chmod(next, 0o755); err != nil {
		_ = os.Remove(next)
		return err
	}
	if err := os.Rename(next, exe); err != nil {
		_ = os.Remove(next)
		return err
	}
	if restart {
		return restartCurrentProcess(exe)
	}
	return nil
}

func installWindowsUpgrade(exe, next string, restart bool) error {
	script := filepath.Join(dataDir(), "apply-upgrade.ps1")
	body := `$ErrorActionPreference="Stop"` + "\n" +
		`Start-Sleep -Seconds 2` + "\n" +
		fmt.Sprintf(`try { Wait-Process -Id %d -Timeout 30 -ErrorAction SilentlyContinue } catch {}`, os.Getpid()) + "\n" +
		"Move-Item -Force " + psQuote(next) + " " + psQuote(exe) + "\n"
	if restart {
		args := ""
		for _, arg := range os.Args[1:] {
			args += " " + psQuote(arg)
		}
		body += "Start-Process -FilePath " + psQuote(exe) + " -ArgumentList " + psQuote(strings.TrimSpace(args)) + "\n"
	}
	body += "Remove-Item -LiteralPath $MyInvocation.MyCommand.Path -Force\n"
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
		return err
	}
	cmd := execCommand("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-File", script)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	if !restart {
		return nil
	}
	return errProcessExitForUpgrade
}

var errProcessExitForUpgrade = errors.New("upgrade staged; process is restarting")

func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func restartCurrentProcess(exe string) error {
	if hostOS == "windows" {
		return nil
	}
	return execSelfFn(exe, os.Args, os.Environ())
}

func managedUpgrade(restart bool) (bool, error) {
	exe, _ := executable()
	lower := strings.ToLower(strings.ReplaceAll(filepath.ToSlash(exe), `\`, "/"))
	var out []byte
	var err error
	switch {
	case strings.Contains(lower, "/cellar/") || strings.Contains(lower, "/homebrew/"):
		formula := "gautampachnanda101/tap/gcse-revision"
		brew, brewErr := findHomebrew()
		if brewErr != nil {
			return false, brewErr
		}
		_, _ = execCommand(brew, "update", "--quiet").CombinedOutput()
		out, err = execCommand(brew, "outdated", "--quiet", formula).CombinedOutput()
		if err == nil && strings.TrimSpace(string(out)) == "" {
			return false, nil
		}
		out, err = execCommand(brew, "upgrade", formula).CombinedOutput()
	case strings.Contains(lower, "/scoop/apps/"):
		out, err = execCommand("scoop", "status", appName).CombinedOutput()
		if err == nil && !strings.Contains(strings.ToLower(string(out)), "update") {
			return false, nil
		}
		if hostOS == "windows" {
			return stageScoopUpgrade(exe, restart)
		}
		out, err = execCommand("scoop", "update", appName).CombinedOutput()
	default:
		return false, errors.New("managed installation type is not supported")
	}
	if err != nil {
		return false, fmt.Errorf("package manager upgrade: %s: %w", strings.TrimSpace(string(out)), err)
	}
	if restart {
		if strings.Contains(lower, "/cellar/") || strings.Contains(lower, "/homebrew/") {
			if err := startHomebrewPackageService(); err != nil {
				return false, err
			}
			return true, errProcessExitForUpgrade
		}
		current, locateErr := installedPackageExecutable()
		if locateErr != nil {
			return false, locateErr
		}
		installOut, installErr := execCommand(current, "service", "install").CombinedOutput()
		if installErr != nil {
			return false, fmt.Errorf("install upgraded background service: %s: %w", strings.TrimSpace(string(installOut)), installErr)
		}
		return true, errProcessExitForUpgrade
	}
	return true, nil
}

func startHomebrewPackageService() error {
	brew, err := findHomebrew()
	if err != nil {
		return err
	}
	out, err := execCommand(brew, "services", "start", "gautampachnanda101/tap/gcse-revision").CombinedOutput()
	if err != nil {
		return fmt.Errorf("start Homebrew service after upgrade: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func findHomebrew() (string, error) {
	for _, candidate := range homebrewBinaries {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	path, err := lookPath("brew")
	if err != nil {
		return "", errors.New("Homebrew is installed but the brew command is unavailable to the background service")
	}
	return path, nil
}

func stageScoopUpgrade(exe string, restart bool) (bool, error) {
	script := filepath.Join(dataDir(), "apply-scoop-upgrade.ps1")
	body := `$ErrorActionPreference="Stop"` + "\n" +
		fmt.Sprintf(`$Log=%s`, psQuote(serviceLogPath())) + "\n" +
		`Start-Sleep -Seconds 2` + "\n" +
		fmt.Sprintf(`try { Wait-Process -Id %d -Timeout 30 -ErrorAction SilentlyContinue } catch {}`, os.Getpid()) + "\n" +
		`try {` + "\n" +
		`  scoop update gcse-revision 2>&1 | Out-File -Append -FilePath $Log` + "\n" +
		`  $NewExe = Join-Path (scoop prefix gcse-revision) "gcse-revision.exe"` + "\n" +
		`  & $NewExe service install 2>&1 | Out-File -Append -FilePath $Log` + "\n" +
		`  "Scoop upgrade completed" | Out-File -Append -FilePath $Log` + "\n" +
		`} catch {` + "\n" +
		`  "Scoop upgrade failed: $($_.Exception.Message)" | Out-File -Append -FilePath $Log` + "\n" +
		"}\n"
	body += "Remove-Item -LiteralPath $MyInvocation.MyCommand.Path -Force\n"
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
		return false, err
	}
	cmd := execCommand("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-File", script)
	if err := cmd.Start(); err != nil {
		return false, err
	}
	go cmd.Wait()
	logf("Scoop upgrade has been scheduled; see '%s service logs' for the result.", appName)
	if restart {
		return true, errProcessExitForUpgrade
	}
	return true, nil
}
