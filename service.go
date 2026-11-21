package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
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

func serviceLogPath() string { return filepath.Join(serviceDir(), appName+".log") }

func serviceStatus() (bool, string, error) {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("launchctl", "list", serviceLabel()).CombinedOutput()
		return err == nil, strings.TrimSpace(string(out)), err
	case "linux":
		out, err := exec.Command("systemctl", "--user", "is-active", serviceUnit()).CombinedOutput()
		return err == nil && strings.TrimSpace(string(out)) == "active", strings.TrimSpace(string(out)), err
	case "windows":
		out, err := exec.Command("schtasks", "/Query", "/TN", serviceName, "/FO", "LIST").CombinedOutput()
		detail := strings.TrimSpace(string(out))
		return err == nil && strings.Contains(strings.ToLower(detail), "running"), detail, err
	default:
		return false, "", fmt.Errorf("background service is not supported on %s", runtime.GOOS)
	}
}

func serviceUnit() string  { return appName + ".service" }
func serviceLabel() string { return "com.gautampachnanda101.gcse-revision" }

func serviceCommand(args []string, opts options) error {
	if len(args) == 0 {
		return errors.New("usage: gcse-revision service <install|start|stop|restart|status|logs|uninstall>")
	}
	switch args[0] {
	case "install":
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
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(serviceDir(), 0o755); err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		u, err := user.Current()
		if err != nil {
			return err
		}
		home, err := os.UserHomeDir()
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
		home, err := os.UserHomeDir()
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
		if out, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl daemon-reload: %s: %w", out, err)
		}
		if out, err := exec.Command("systemctl", "--user", "enable", "--now", serviceUnit()).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl enable: %s: %w", out, err)
		}
	case "windows":
		_ = exec.Command("schtasks", "/End", "/TN", serviceName).Run()
		time.Sleep(500 * time.Millisecond)
		tr := fmt.Sprintf(`"%s" serve --service --no-browser --no-ai --port %d`, exe, opts.port)
		if out, err := exec.Command("schtasks", "/Create", "/TN", serviceName, "/SC", "ONLOGON", "/RL", "LIMITED", "/F", "/TR", tr).CombinedOutput(); err != nil {
			return fmt.Errorf("schtasks create: %s: %w", out, err)
		}
		if out, err := exec.Command("schtasks", "/Run", "/TN", serviceName).CombinedOutput(); err != nil {
			return fmt.Errorf("schtasks run: %s: %w", out, err)
		}
	default:
		return fmt.Errorf("background service is not supported on %s", runtime.GOOS)
	}
	fmt.Printf("installed  %s\n", browserURL(opts.port))
	return nil
}

func startService() error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		plist := filepath.Join(home, "Library", "LaunchAgents", serviceLabel()+".plist")
		out, err := exec.Command("launchctl", "bootstrap", "gui/"+currentUserID(), plist).CombinedOutput()
		if err == nil {
			return nil
		}
		out2, err2 := exec.Command("launchctl", "load", "-w", plist).CombinedOutput()
		if err2 != nil {
			return fmt.Errorf("start service: bootstrap: %s: %v; load: %s: %w", out, err, out2, err2)
		}
		return nil
	case "linux":
		cmd = exec.Command("systemctl", "--user", "start", serviceUnit())
	case "windows":
		cmd = exec.Command("schtasks", "/Run", "/TN", serviceName)
	default:
		return fmt.Errorf("background service is not supported on %s", runtime.GOOS)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("start service: %s: %w", out, err)
	}
	return nil
}

func stopService() error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("launchctl", "bootout", "gui/"+currentUserID()+"/"+serviceLabel())
	case "linux":
		cmd = exec.Command("systemctl", "--user", "stop", serviceUnit())
	case "windows":
		cmd = exec.Command("schtasks", "/End", "/TN", serviceName)
	default:
		return fmt.Errorf("background service is not supported on %s", runtime.GOOS)
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
	deadline := time.Now().Add(8 * time.Second)
	for {
		running, _, _ := serviceStatus()
		listening := alreadyRunning(net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if !running && !listening {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("background service did not stop in time")
		}
		time.Sleep(150 * time.Millisecond)
	}
	var last error
	for i := 0; i < 5; i++ {
		if err := startService(); err == nil {
			healthDeadline := time.Now().Add(12 * time.Second)
			for time.Now().Before(healthDeadline) {
				if alreadyRunning(net.JoinHostPort("127.0.0.1", strconv.Itoa(port))) {
					return nil
				}
				time.Sleep(250 * time.Millisecond)
			}
			last = fmt.Errorf("service started but %s is not responding", browserURL(port))
		} else {
			last = err
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("background service did not start after upgrade: %w", last)
}

func uninstallService() error {
	switch runtime.GOOS {
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		path := filepath.Join(home, "Library", "LaunchAgents", serviceLabel()+".plist")
		_ = os.Remove(path)
	case "linux":
		if out, err := exec.Command("systemctl", "--user", "disable", serviceUnit()).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl disable: %s: %w", out, err)
		}
		home, _ := os.UserHomeDir()
		_ = os.Remove(filepath.Join(home, ".config", "systemd", "user", serviceUnit()))
		_, _ = exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput()
	case "windows":
		if out, err := exec.Command("schtasks", "/Delete", "/TN", serviceName, "/F").CombinedOutput(); err != nil && !serviceNotFound(string(out)) {
			return fmt.Errorf("schtasks delete: %s: %w", out, err)
		}
	default:
		return fmt.Errorf("background service is not supported on %s", runtime.GOOS)
	}
	return nil
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
	u, err := user.Current()
	if err == nil && u.Uid != "" {
		return u.Uid
	}
	return strconv.Itoa(0)
}
