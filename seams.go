package main

import (
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"time"

	"github.com/zalando/go-keyring"
)

// Test seams. Production code reaches the operating system through these variables instead of calling runtime,
// os/exec, os/user or time directly, so unit tests can exercise every platform branch (launchd, systemd, Task
// Scheduler, Homebrew, Scoop, Ollama installs) on any host without touching the real system. The defaults are
// the real implementations; only tests reassign them.
var (
	hostOS             = runtime.GOOS
	hostArch           = runtime.GOARCH
	executable         = os.Executable
	currentUser        = user.Current
	execCommand        = exec.Command
	execCommandContext = exec.CommandContext
	lookPath           = exec.LookPath
	sleep              = time.Sleep
	exitProcess        = os.Exit
	execSelfFn         = execSelf
	bakedPrivatePacks  = embeddedPrivatePacks // packs compiled in from ./private (empty in public builds)
	listen             = net.Listen
	randReader         = rand.Reader
	userConfigDir      = os.UserConfigDir
	keyringGet         = keyring.Get
	keyringSet         = keyring.Set
	keyringDelete      = keyring.Delete
	marshalCloudJSON   = json.Marshal
	newCloudRequest    = http.NewRequestWithContext
	rename             = os.Rename
	chmod              = os.Chmod

	// Remote endpoints.
	githubAPIBase    = "https://api.github.com"
	ollamaReleaseURL = "https://github.com/ollama/ollama/releases/latest/download/"

	// Well-known install locations.
	homebrewBinaries       = []string{"/opt/homebrew/bin/brew", "/usr/local/bin/brew", "/home/linuxbrew/.linuxbrew/bin/brew"}
	homebrewAppExecutables = []string{"/opt/homebrew/bin/gcse-revision", "/usr/local/bin/gcse-revision", "/home/linuxbrew/.linuxbrew/bin/gcse-revision"}
	procMeminfo            = "/proc/meminfo"

	// Timings.
	serviceStopTimeout     = 8 * time.Second
	serviceHealthTimeout   = 12 * time.Second
	ollamaStartPolls       = 60
	ollamaPollInterval     = 500 * time.Millisecond
	upgradeFirstCheck      = 15 * time.Second
	upgradeInterval        = 24 * time.Hour
	managedUpgradePoll     = 15 * time.Second
	privateInstallLockWait = 30 * time.Second
)
