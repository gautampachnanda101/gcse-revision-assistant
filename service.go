package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const serviceName = "GCSERevision"

var serviceLog *os.File

func enableServiceLogging() error {
	if err := os.MkdirAll(serviceDir(), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(serviceLogPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	serviceLog = f
	return nil
}

func closeServiceLogging() {
	if serviceLog != nil {
		_ = serviceLog.Close()
		serviceLog = nil
	}
}

func serviceDir() string {
	return filepath.Join(dataDir(), "service")
}

func realUserHomeDir() (string, error) {
	u, err := currentUser()
	if err == nil && strings.TrimSpace(u.HomeDir) != "" {
		return u.HomeDir, nil
	}
	return os.UserHomeDir()
}

func serviceLogPath() string { return filepath.Join(serviceDir(), appName+".log") }

func serviceStatus() (bool, string, error) {
	switch hostOS {
	case "darwin":
		var lastErr error
		for _, label := range serviceLabels() {
			out, err := execCommand("launchctl", "list", label).CombinedOutput()
			if err == nil {
				return true, strings.TrimSpace(string(out)), nil
			}
			lastErr = err
		}
		return false, "", lastErr
	case "linux":
		out, err := execCommand("systemctl", "--user", "is-active", serviceUnit()).CombinedOutput()
		return err == nil && strings.TrimSpace(string(out)) == "active", strings.TrimSpace(string(out)), err
	case "windows":
		out, err := execCommand("schtasks", "/Query", "/TN", serviceName, "/FO", "LIST").CombinedOutput()
		detail := strings.TrimSpace(string(out))
		return err == nil && strings.Contains(strings.ToLower(detail), "running"), detail, err
	default:
		return false, "", fmt.Errorf("background service is not supported on %s", hostOS)
	}
}

func serviceUnit() string  { return appName + ".service" }
func serviceLabel() string { return "com.gautampachnanda101.gcse-revision" }

func serviceLabels() []string {
	return []string{serviceLabel(), "sh.brew.gcse-revision", "homebrew.mxcl.gcse-revision"}
}

func serviceCommand(args []string, opts options) error {
	if len(args) == 0 {
		return errors.New("usage: gcse-revision service <install|ensure|start|stop|restart|status|logs|uninstall>")
	}
	switch args[0] {
	case "install":
		return installService(opts)
	case "ensure":
		configured, err := serviceConfigured()
		if err != nil {
			return err
		}
		if !configured {
			fmt.Println("background service not configured; no action needed")
			return nil
		}
		return installService(opts)
	case "start":
		return startService()
	case "stop":
		return stopService()
	case "restart":
		return restartServiceAt(opts.port)
	case "status":
		running, _, _ := serviceStatus()
		if running {
			fmt.Printf("running  %s\n", browserURL(opts.port))
		} else {
			fmt.Println("stopped  The background service is not installed or is not running.")
		}
		return nil
	case "logs":
		return showServiceLogs()
	case "uninstall":
		_ = stopService()
		return uninstallService()
	default:
		return fmt.Errorf("unknown service command %q", args[0])
	}
}

func installService(opts options) error {
	if hostOS == "darwin" && usesHomebrewService() {
		return restartServiceAt(opts.port)
	}
	exe, err := executable()
	if err != nil {
		return err
	}

	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(serviceDir(), 0o755); err != nil {
		return err
	}
	switch hostOS {
	case "darwin":
		u, err := currentUser()
		if err != nil {
			return err
		}
		home, err := realUserHomeDir()
		if err != nil {
			return err
		}
		plistPath := filepath.Join(home, "Library", "LaunchAgents", serviceLabel()+".plist")
		plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array><string>%s</string><string>serve</string><string>--service</string><string>--no-browser</string><string>--no-ai</string><string>--port</string><string>%d</string></array>
  <key>UserName</key><string>%s</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict></plist>
`, serviceLabel(), xmlEscape(exe), opts.port, xmlEscape(u.Username))
		if err := writeFileAtomic(plistPath, []byte(plist)); err != nil {
			return err
		}
		if err := restartServiceAt(opts.port); err != nil {
			return fmt.Errorf("start installed launch agent: %w", err)
		}
	case "linux":
		home, err := realUserHomeDir()
		if err != nil {
			return err
		}
		unitPath := filepath.Join(home, ".config", "systemd", "user", serviceUnit())
		unit := fmt.Sprintf(`[Unit]
Description=GCSE Revision background service
After=network-online.target

[Service]
ExecStart=%s serve --service --no-browser --no-ai --port %d
Restart=always
RestartSec=3
StandardOutput=null
StandardError=null

[Install]
WantedBy=default.target
`, shellQuote(exe), opts.port)
		if err := writeFileAtomic(unitPath, []byte(unit)); err != nil {
			return err
		}
		if out, err := execCommand("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl daemon-reload: %s: %w", out, err)
		}
		if out, err := execCommand("systemctl", "--user", "enable", "--now", serviceUnit()).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl enable: %s: %w", out, err)
		}
	case "windows":
		_ = execCommand("schtasks", "/End", "/TN", serviceName).Run()
		sleep(500 * time.Millisecond)
		tr := fmt.Sprintf(`"%s" serve --service --no-browser --no-ai --port %d`, exe, opts.port)
		if out, err := execCommand("schtasks", "/Create", "/TN", serviceName, "/SC", "ONLOGON", "/RL", "LIMITED", "/F", "/TR", tr).CombinedOutput(); err != nil {
			return fmt.Errorf("schtasks create: %s: %w", out, err)
		}
		if out, err := execCommand("schtasks", "/Run", "/TN", serviceName).CombinedOutput(); err != nil {
			return fmt.Errorf("schtasks run: %s: %w", out, err)
		}
	default:
		return fmt.Errorf("background service is not supported on %s", hostOS)
	}
	fmt.Printf("installed  %s\n", browserURL(opts.port))
	return nil
}

func serviceConfigured() (bool, error) {
	switch hostOS {
	case "darwin":
		if usesHomebrewService() {
			return true, nil
		}
		home, err := realUserHomeDir()
		if err != nil {
			return false, err
		}
		_, err = os.Stat(filepath.Join(home, "Library", "LaunchAgents", serviceLabel()+".plist"))
		return err == nil, ignoreMissing(err)
	case "linux":
		home, err := realUserHomeDir()
		if err != nil {
			return false, err
		}
		_, err = os.Stat(filepath.Join(home, ".config", "systemd", "user", serviceUnit()))
		return err == nil, ignoreMissing(err)
	case "windows":
		err := execCommand("schtasks", "/Query", "/TN", serviceName).Run()
		return err == nil, nil
	default:
		return false, fmt.Errorf("background service is not supported on %s", hostOS)
	}
}

func ignoreMissing(err error) error {
	if errors.Is(err, os.ErrNotExist) || err == nil {
		return nil
	}
	return err
}

func startService() error {
	var cmd *exec.Cmd
	switch hostOS {
	case "darwin":
		if usesHomebrewService() {
			brew, err := findHomebrew()
			if err != nil {
				return err
			}
			out, err := execCommand(brew, "services", "start", "gautampachnanda101/tap/gcse-revision").CombinedOutput()
			if err != nil {
				return fmt.Errorf("start Homebrew service: %s: %w", out, err)
			}
			return nil
		}
		home, _ := realUserHomeDir()
		plist := filepath.Join(home, "Library", "LaunchAgents", serviceLabel()+".plist")
		out, err := execCommand("launchctl", "bootstrap", "gui/"+currentUserID(), plist).CombinedOutput()
		if err == nil {
			return nil
		}
		out2, err2 := execCommand("launchctl", "load", "-w", plist).CombinedOutput()
		if err2 != nil {
			return fmt.Errorf("start service: bootstrap: %s: %v; load: %s: %w", out, err, out2, err2)
		}
		return nil
	case "linux":
		cmd = execCommand("systemctl", "--user", "start", serviceUnit())
	case "windows":
		cmd = execCommand("schtasks", "/Run", "/TN", serviceName)
	default:
		return fmt.Errorf("background service is not supported on %s", hostOS)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("start service: %s: %w", out, err)
	}
	return nil
}

func stopService() error {
	var cmd *exec.Cmd
	switch hostOS {
	case "darwin":
		if usesHomebrewService() {
			brew, err := findHomebrew()
			if err != nil {
				return err
			}
			out, err := execCommand(brew, "services", "stop", "gautampachnanda101/tap/gcse-revision").CombinedOutput()
			if err != nil && !serviceNotFound(string(out)) {
				return fmt.Errorf("stop Homebrew service: %s: %w", out, err)
			}
			legacy, legacyErr := execCommand("launchctl", "bootout", "gui/"+currentUserID()+"/"+serviceLabel()).CombinedOutput()
			if legacyErr != nil && !serviceNotFound(string(legacy)) {
				return fmt.Errorf("stop legacy launch agent: %s: %w", legacy, legacyErr)
			}
			home, err := realUserHomeDir()
			if err != nil {
				return err
			}
			if err := os.Remove(filepath.Join(home, "Library", "LaunchAgents", serviceLabel()+".plist")); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove legacy launch agent: %w", err)
			}
			return nil
		}
		cmd = execCommand("launchctl", "bootout", "gui/"+currentUserID()+"/"+serviceLabel())
	case "linux":
		cmd = execCommand("systemctl", "--user", "stop", serviceUnit())
	case "windows":
		cmd = execCommand("schtasks", "/End", "/TN", serviceName)
	default:
		return fmt.Errorf("background service is not supported on %s", hostOS)
	}

	out, err := cmd.CombinedOutput()
	if err != nil && !serviceNotFound(string(out)) {
		return fmt.Errorf("stop service: %s: %w", out, err)
	}
	return nil
}

func restartService() error {
	return restartServiceAt(8765)
}

func restartServiceAt(port int) error {
	if err := stopService(); err != nil {
		return err
	}
	deadline := time.Now().Add(serviceStopTimeout)
	for {
		running, _, _ := serviceStatus()
		listening := alreadyRunning(net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if !running && !listening {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("background service did not stop in time")
		}
		sleep(150 * time.Millisecond)
	}
	var last error
	for i := 0; i < 5; i++ {
		if err := startService(); err == nil {
			healthDeadline := time.Now().Add(serviceHealthTimeout)
			for time.Now().Before(healthDeadline) {
				if alreadyRunning(net.JoinHostPort("127.0.0.1", strconv.Itoa(port))) {
					return nil
				}
				sleep(250 * time.Millisecond)
			}
			last = fmt.Errorf("service started but %s is not responding", browserURL(port))
		} else {
			last = err
		}
		sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("background service did not start after upgrade: %w", last)
}

func uninstallService() error {
	switch hostOS {
	case "darwin":
		home, err := realUserHomeDir()
		if err != nil {
			return err
		}
		path := filepath.Join(home, "Library", "LaunchAgents", serviceLabel()+".plist")
		_ = os.Remove(path)
	case "linux":
		if out, err := execCommand("systemctl", "--user", "disable", serviceUnit()).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl disable: %s: %w", out, err)
		}
		home, _ := realUserHomeDir()
		_ = os.Remove(filepath.Join(home, ".config", "systemd", "user", serviceUnit()))
		_, _ = execCommand("systemctl", "--user", "daemon-reload").CombinedOutput()
	case "windows":
		if out, err := execCommand("schtasks", "/Delete", "/TN", serviceName, "/F").CombinedOutput(); err != nil && !serviceNotFound(string(out)) {
			return fmt.Errorf("schtasks delete: %s: %w", out, err)
		}
	default:
		return fmt.Errorf("background service is not supported on %s", hostOS)
	}
	return nil
}

func homebrewServiceConfigured() bool {
	home, err := realUserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, "Library", "LaunchAgents", "sh.brew.gcse-revision.plist"))
	return err == nil
}

func usesHomebrewService() bool {
	if homebrewServiceConfigured() {
		return true
	}
	exe, err := executable()
	if err != nil || hostOS != "darwin" || !packageManagedPath(exe) {
		return false
	}
	path := strings.ToLower(strings.ReplaceAll(filepath.ToSlash(exe), `\`, "/"))
	return strings.Contains(path, "/cellar/") || strings.Contains(path, "/homebrew/")
}

func showServiceLogs() error {
	b, err := os.ReadFile(serviceLogPath())
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("service log is not available yet")
	}
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	start := 0
	if len(lines) > 100 {
		start = len(lines) - 100
	}
	fmt.Println(strings.Join(lines[start:], "\n"))
	return nil
}

func serviceNotFound(s string) bool {
	lower := strings.ToLower(s)
	return strings.Contains(lower, "could not find") || strings.Contains(lower, "not loaded") || strings.Contains(lower, "not found") || strings.Contains(lower, "no such")
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func currentUserID() string {
	u, err := currentUser()
	if err == nil && u.Uid != "" {
		return u.Uid
	}
	return strconv.Itoa(0)
}
