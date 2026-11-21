package main

import (
	"flag"
	"fmt"
	"io"
	"os"
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
