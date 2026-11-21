package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type ansiStyle struct {
	enabled bool
}

func newANSIStyle(w io.Writer) ansiStyle {
	term := strings.ToLower(os.Getenv("TERM"))
	noColor := os.Getenv("NO_COLOR") != ""
	f, ok := w.(*os.File)
	return ansiStyle{enabled: ok && !noColor && term != "dumb" && isTerminal(f)}
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func (s ansiStyle) paint(code, text string) string {
	if !s.enabled {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s ansiStyle) bold(text string) string   { return s.paint("1", text) }
func (s ansiStyle) cyan(text string) string   { return s.paint("36", text) }
func (s ansiStyle) green(text string) string  { return s.paint("32", text) }
func (s ansiStyle) yellow(text string) string { return s.paint("33", text) }
func (s ansiStyle) dim(text string) string    { return s.paint("2", text) }

func writeUsage(w io.Writer) {
	s := newANSIStyle(w)
	fmt.Fprintf(w, "%s %s — %s\n\n", s.bold(appName), s.cyan(version), "offline GCSE revision with local AI")
	fmt.Fprintf(w, "%s\n", s.bold("Usage"))
	fmt.Fprintf(w, "  %-40s %s\n", s.green(appName+" [flags]"), "start the app and open the browser")
	fmt.Fprintf(w, "  %-40s %s\n", s.green(appName+" service <command>"), "manage the background service")
	fmt.Fprintf(w, "  %-40s %s\n", s.green(appName+" update"), "download updated subject packs")
	fmt.Fprintf(w, "  %-40s %s\n", s.green(appName+" upgrade"), "check for and install a new application release")
	fmt.Fprintf(w, "  %-40s %s\n", s.green(appName+" setup-ai"), "install the local AI helper and model")
	fmt.Fprintf(w, "  %-40s %s\n", s.green(appName+" doctor"), "check the installation, service and local folders")
	fmt.Fprintf(w, "  %-40s %s\n", s.green(appName+" completion <shell>"), "print shell completion for bash, zsh, fish or PowerShell")
	fmt.Fprintf(w, "  %-40s %s\n", s.green(appName+" import <file|folder|zip>"), "install a private subject pack")
	fmt.Fprintf(w, "  %-40s %s\n", s.green(appName+" export <pack.js> [out]"), "export a subject pack")
	fmt.Fprintf(w, "  %-40s %s\n", s.green(appName+" version"), "print the installed version")

	fmt.Fprintf(w, "\n%s\n", s.bold("Service commands"))
	fmt.Fprintf(w, "  %s\n", s.cyan("install  start  stop  restart  status  logs  uninstall"))
	fmt.Fprintf(w, "  %-30s %s\n", s.dim(appName+" service status"), "check whether the background app is running")
	fmt.Fprintf(w, "  The app runs in the background and opens at %s.\n", s.green(browserURL(8765)))

	fmt.Fprintf(w, "\n%s\n", s.bold("Maintainer commands"))
	fmt.Fprintf(w, "  %s\n", s.dim("build  validate  manifest  sign  verify  keygen  encrypt-private"))

	fmt.Fprintf(w, "\n%s\n", s.bold("Flags"))
	old := flag.CommandLine.Output()
	flag.CommandLine.SetOutput(w)
	flag.PrintDefaults()
	flag.CommandLine.SetOutput(old)
	fmt.Fprintf(w, "\n%s %s\n", s.yellow("Private packs:"), privatePacksDir())
	fmt.Fprintln(w, "Tip: set NO_COLOR=1 for plain help output.")
}

func helpTopic(w io.Writer, topic string) error {
	s := newANSIStyle(w)
	switch strings.ToLower(topic) {
	case "service":
		fmt.Fprintf(w, "%s\n\n", s.bold("Background service"))
		fmt.Fprintf(w, "  %s\n", s.cyan(appName+" service install"))
		fmt.Fprintln(w, "  Installs and starts a per-user service, opening at http://gcse-revision.localhost:8765/.")
		fmt.Fprintln(w, "  Other commands: start, stop, restart, status, logs, uninstall.")
		fmt.Fprintln(w, "  The service starts after login and keeps running in the background.")
	case "update", "upgrade":
		fmt.Fprintf(w, "%s\n\n", s.bold("Updates"))
		fmt.Fprintf(w, "  %s  %s\n", s.cyan("update"), "refresh signed subject packs without changing the application")
		fmt.Fprintf(w, "  %s %s\n", s.cyan("upgrade"), "check/install a new application release")
		fmt.Fprintln(w, "  Homebrew and Scoop installations use their package manager automatically.")
		fmt.Fprintln(w, "  Set GCSE_REVISION_AUTO_UPDATE=0 or use --no-auto-update to disable checks.")
	case "completion":
		fmt.Fprintf(w, "%s\n\n", s.bold("Shell completion"))
		fmt.Fprintln(w, "  Bash:        source <(gcse-revision completion bash)")
		fmt.Fprintln(w, "  Zsh:         source <(gcse-revision completion zsh)")
		fmt.Fprintln(w, "  Fish:        gcse-revision completion fish | source")
		fmt.Fprintln(w, "  PowerShell:  gcse-revision completion powershell | Out-String | Invoke-Expression")
	default:
		return fmt.Errorf("no help topic named %q; try: service, update, upgrade or completion", topic)
	}
	return nil
}

func writeCompletion(w io.Writer, shell string) error {
	commands := "serve run service update upgrade setup-ai doctor completion version help import export build validate manifest sign verify"
	service := "install start stop restart status logs uninstall"
	switch strings.ToLower(shell) {
	case "bash":
		_, _ = fmt.Fprintf(w, `_gcse_revision_complete() {
  local cur prev
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  if [[ "$prev" == "service" ]]; then COMPREPLY=( $(compgen -W "%s" -- "$cur") ); return; fi
  COMPREPLY=( $(compgen -W "%s --help --offline --no-ai --no-browser --no-auto-update --port --repo --branch" -- "$cur") )
}
complete -F _gcse_revision_complete gcse-revision
`, service, commands)
	case "zsh":
		_, _ = fmt.Fprintf(w, `#compdef gcse-revision
_gcse-revision() {
  local -a cmds services
  cmds=(%s)
  services=(%s)
  if (( CURRENT == 3 )) && [[ "$words[2]" == service ]]; then
    _describe 'service command' services
  else
    _describe 'command' cmds
  fi
}
compdef _gcse-revision gcse-revision
`, commands, service)
	case "fish":
		_, _ = fmt.Fprintf(w, `complete -c gcse-revision -f
complete -c gcse-revision -n '__fish_use_subcommand' -a '%s'
complete -c gcse-revision -n '__fish_seen_subcommand_from service' -a '%s'
complete -c gcse-revision -l offline -d 'Work without network access'
complete -c gcse-revision -l no-ai -d 'Do not install or start the AI helper'
complete -c gcse-revision -l no-browser -d 'Do not open a browser'
complete -c gcse-revision -l no-auto-update -d 'Disable application auto-update'
`, commands, service)
	case "powershell", "pwsh":
		_, _ = fmt.Fprint(w, `Register-ArgumentCompleter -Native -CommandName gcse-revision -ScriptBlock {
  param($wordToComplete, $commandAst, $cursorPosition)
  $commands = @('serve','run','service','update','upgrade','setup-ai','doctor','completion','version','help','import','export','build','validate','manifest','sign','verify')
  $services = @('install','start','stop','restart','status','logs','uninstall')
  $tokens = $commandAst.ToString().Trim().Split()
  $choices = if ($tokens.Count -gt 1 -and $tokens[1] -eq 'service') { $services } else { $commands + @('--help','--offline','--no-ai','--no-browser','--no-auto-update','--port','--repo','--branch') }
  $choices | Where-Object { $_ -like "$wordToComplete*" } | ForEach-Object {
    [System.Management.Automation.CompletionResult]::new($_, $_, 'ParameterValue', $_)
  }
}
`)
	default:
		return fmt.Errorf("completion shell must be bash, zsh, fish or powershell")
	}
	return nil
}

func runDoctor(opts options) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.Abs(exe)
	s := newANSIStyle(os.Stdout)
	fmt.Printf("%-20s %s\n", s.dim("Version"), appName+" "+version)
	fmt.Printf("%-20s %s/%s\n", s.dim("Platform"), runtime.GOOS, runtime.GOARCH)
	fmt.Printf("%-20s %s\n", s.dim("Executable"), exe)
	if packageManagedPath(exe) {
		fmt.Printf("%-20s %s\n", s.dim("Installation"), s.green("package-managed"))
	} else {
		fmt.Printf("%-20s %s\n", s.dim("Installation"), "standalone")
	}
	running, _, _ := serviceStatus()
	state, colour := "stopped", s.yellow("stopped")
	if running {
		state, colour = "running", s.green("running")
	}
	_ = state
	fmt.Printf("%-20s %s\n", s.dim("Background service"), colour)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(opts.port))
	healthy := alreadyRunning(addr)
	fmt.Printf("%-20s %s (%s)\n", s.dim("App"), map[bool]string{true: s.green("healthy"), false: s.yellow("not running")}[healthy], browserURL(opts.port))
	fmt.Printf("%-20s %s\n", s.dim("Cache"), dataDir())
	fmt.Printf("%-20s %s\n", s.dim("Private packs"), privatePacksDir())
	autoUpgrade := envOr("GCSE_REVISION_AUTO_UPDATE", "1") == "1" && !opts.noAutoUp && !opts.offline
	if _, err := versionParts(version); err != nil {
		autoUpgrade = false
	}
	fmt.Printf("%-20s %s\n", s.dim("Auto-upgrade"), map[bool]string{true: s.green("enabled"), false: s.yellow("disabled")}[autoUpgrade])
	return nil
}
