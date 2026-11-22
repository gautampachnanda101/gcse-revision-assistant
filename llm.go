package main

// Local AI helper: find → (install) → start → pick/pull model → proxy.
//
//  1. If an Ollama server already answers (OLLAMA_HOST or 127.0.0.1:11434), use it.
//  2. Else find an ollama binary (PATH, the desktop app, or our own private copy) and start `ollama serve`.
//  3. Else download the official Ollama release for this OS/arch into the app's cache folder (no admin rights,
//     checksum-verified), and start it. Linux release archives are .tar.zst, so that path uses the system
//     `zstd` tool, falling back to Ollama's official install script.
//  4. Pick a model sized to this machine's RAM (or reuse a suitable one already downloaded) and pull it.
//
// The page talks to Ollama through /llm/ on our own origin, so browser CORS rules never get in the way.

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	phaseIdle       = "idle"
	phaseDisabled   = "disabled"
	phaseChecking   = "checking"
	phaseInstalling = "installing"
	phaseStarting   = "starting"
	phasePulling    = "pulling"
	phaseReady      = "ready"
	phaseError      = "error"
)

type aiStatus struct {
	Phase    string  `json:"phase"`
	Message  string  `json:"message"`
	Progress float64 `json:"progress"` // 0..1 for downloads, -1 when unknown
	Model    string  `json:"model,omitempty"`
	Backend  string  `json:"backend"`
	Proxy    string  `json:"proxy"` // path on this server the page should use as its Ollama base
	RAMGB    int     `json:"ramGB,omitempty"`
}

type aiManager struct {
	mu        sync.Mutex
	st        aiStatus
	running   sync.Mutex // serialises ensure()
	base      *url.URL
	wantModel string
	online    bool
	child     *exec.Cmd
}

func newAIManager(model string, online bool) *aiManager {
	host := envOr("OLLAMA_HOST", "127.0.0.1:11434")
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	u, err := url.Parse(host)
	if err != nil || u.Host == "" {
		u, _ = url.Parse("http://127.0.0.1:11434")
	}
	if h := u.Hostname(); h == "0.0.0.0" || h == "" {
		u.Host = "127.0.0.1:" + orDefault(u.Port(), "11434")
	}
	return &aiManager{
		base: u, wantModel: model, online: online,
		st: aiStatus{Phase: phaseIdle, Message: "Not started yet.", Backend: "Ollama", Proxy: "/llm", Progress: -1},
	}
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func (a *aiManager) set(phase, msg string, progress float64) {
	a.mu.Lock()
	a.st.Phase, a.st.Message, a.st.Progress = phase, msg, progress
	a.mu.Unlock()
	if phase != phasePulling && phase != phaseInstalling || progress < 0 {
		logf("AI helper: %s", msg)
	}
}

func (a *aiManager) status() aiStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.st
}

func (a *aiManager) disable() {
	a.set(phaseDisabled, "AI helper set-up is switched off (started with --no-ai). Click “Find my AI helper” to set it up now.", -1)
}
func (a *aiManager) enable() {
	if a.status().Phase == phaseDisabled {
		a.set(phaseIdle, "Starting…", -1)
	}
}

func (a *aiManager) proxy() http.Handler {
	rp := httputil.NewSingleHostReverseProxy(a.base)
	rp.FlushInterval = -1 // stream responses as they arrive
	orig := rp.Director
	rp.Director = func(r *http.Request) {
		orig(r)
		r.URL.Path = "/" + strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/"), "llm/")
		r.Host = a.base.Host
		// Ollama rejects browser origins it doesn't know; this request comes from us, not the browser.
		r.Header.Del("Origin")
		r.Header.Del("Referer")
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		http.Error(w, "AI helper is not running yet: "+a.status().Message, http.StatusBadGateway)
	}
	return http.StripPrefix("", rp)
}

func (a *aiManager) shutdown() {
	a.mu.Lock()
	c := a.child
	a.mu.Unlock()
	if c != nil && c.Process != nil {
		_ = c.Process.Kill()
	}
}

// ensure walks the whole set-up. Safe to call repeatedly; concurrent calls wait for the first.
func (a *aiManager) ensure(ctx context.Context) {
	if a.status().Phase == phaseDisabled {
		return
	}
	a.running.Lock()
	defer a.running.Unlock()
	if a.status().Phase == phaseReady && a.serverUp(ctx) {
		return
	}
	if err := a.ensureServer(ctx); err != nil {
		a.set(phaseError, err.Error(), -1)
		return
	}
	model, err := a.ensureModel(ctx)
	if err != nil {
		a.set(phaseError, err.Error(), -1)
		return
	}
	a.mu.Lock()
	a.st.Model = model
	a.mu.Unlock()
	a.set(phaseReady, "Ready — using "+model+".", 1)
}

func (a *aiManager) api(p string) string { return strings.TrimRight(a.base.String(), "/") + p }

func (a *aiManager) serverUp(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.api("/api/version"), nil)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	r.Body.Close()
	return r.StatusCode == http.StatusOK
}

func (a *aiManager) ensureServer(ctx context.Context) error {
	a.set(phaseChecking, "Looking for the AI helper on this computer…", -1)
	if a.serverUp(ctx) {
		return nil
	}
	bin := findOllama()
	if bin == "" {
		if !a.online {
			return errors.New("the AI helper (Ollama) isn't installed, and the app was started with --offline, so it can't be downloaded")
		}
		var err error
		if bin, err = a.installOllama(ctx); err != nil {
			return fmt.Errorf("couldn't install the AI helper: %w", err)
		}
	}
	a.set(phaseStarting, "Starting the AI helper…", -1)
	cmd := execCommand(bin, "serve")
	cmd.Env = append(os.Environ(), "OLLAMA_HOST="+a.base.Host)
	logPath := filepath.Join(dataDir(), "ollama.log")
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		cmd.Stdout, cmd.Stderr = f, f
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("couldn't start %s: %w", bin, err)
	}
	a.mu.Lock()
	a.child = cmd
	a.mu.Unlock()
	go cmd.Wait()
	for i := 0; i < ollamaStartPolls; i++ {
		if a.serverUp(ctx) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(ollamaPollInterval):
		}
	}
	return fmt.Errorf("the AI helper didn't start within 30 seconds — see %s", logPath)
}

func exeName(n string) string {
	if hostOS == "windows" {
		return n + ".exe"
	}
	return n
}

func privateOllamaDir() string { return filepath.Join(dataDir(), "ollama") }

func findOllama() string {
	if os.Getenv("GCSE_REVISION_PRIVATE_OLLAMA") == "1" { // testing: ignore any system install
		return findFile(privateOllamaDir(), exeName("ollama"))
	}
	if p, err := lookPath("ollama"); err == nil {
		return p
	}
	cands := systemOllamaPaths()
	if p := findFile(privateOllamaDir(), exeName("ollama")); p != "" {
		cands = append(cands, p)
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// systemOllamaPaths lists where the Ollama desktop app and package managers put the program (a seam for tests).
var systemOllamaPaths = systemOllamaPathsDefault

func systemOllamaPathsDefault() []string {
	home, _ := os.UserHomeDir()
	switch hostOS {
	case "darwin":
		return []string{"/Applications/Ollama.app/Contents/Resources/ollama",
			filepath.Join(home, "Applications/Ollama.app/Contents/Resources/ollama"),
			"/opt/homebrew/bin/ollama", "/usr/local/bin/ollama"}
	case "windows":
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			return []string{filepath.Join(la, `Programs\Ollama\ollama.exe`)}
		}
		return nil
	default:
		return []string{"/usr/local/bin/ollama", "/usr/bin/ollama"}
	}
}

func findFile(root, name string) string {
	found := ""
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == name {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func (a *aiManager) installOllama(ctx context.Context) (string, error) {
	arch := hostArch
	var asset string
	switch hostOS {
	case "darwin":
		asset = "ollama-darwin.tgz"
	case "windows":
		asset = "ollama-windows-" + arch + ".zip"
	case "linux":
		if _, err := lookPath("zstd"); err != nil {
			return a.installOllamaScript(ctx)
		}
		asset = "ollama-linux-" + arch + ".tar.zst"
	default:
		return "", fmt.Errorf("no Ollama build for %s — install it from https://ollama.com", hostOS)
	}
	sums, err := httpGet(ctx, ollamaReleaseURL+"sha256sum.txt", 1<<20)
	if err != nil {
		return "", err
	}
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "./") == asset {
			want = f[0]
		}
	}
	if want == "" {
		return "", fmt.Errorf("no published checksum for %s", asset)
	}

	tmp, err := os.CreateTemp("", "ollama-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	size, err := a.download(ctx, ollamaReleaseURL+asset, tmp, want, "Downloading the AI helper (Ollama)")
	if err != nil {
		return "", err
	}
	archive := io.NewSectionReader(tmp, 0, size)

	a.set(phaseInstalling, "Unpacking the AI helper…", -1)
	dest := privateOllamaDir()
	_ = os.RemoveAll(dest)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	switch {
	case strings.HasSuffix(asset, ".tgz"):
		gz, err := gzip.NewReader(archive)
		if err != nil {
			return "", err
		}
		err = untar(gz, dest)
		if err != nil {
			return "", err
		}
	case strings.HasSuffix(asset, ".zip"):
		if err := unzip(archive, size, dest); err != nil {
			return "", err
		}
	case strings.HasSuffix(asset, ".tar.zst"):
		zcmd := execCommandContext(ctx, "zstd", "-dc", tmp.Name())
		out, err := zcmd.StdoutPipe()
		if err != nil {
			return "", err
		}
		if err := zcmd.Start(); err != nil {
			return "", err
		}
		if err := untar(out, dest); err != nil {
			_ = zcmd.Process.Kill()
			return "", err
		}
		if err := zcmd.Wait(); err != nil {
			return "", err
		}
	}
	bin := findFile(dest, exeName("ollama"))
	if bin == "" {
		return "", errors.New("downloaded archive didn't contain the ollama program")
	}
	_ = os.Chmod(bin, 0o755)
	return bin, nil
}

// installOllamaScript: Linux without zstd — use Ollama's official installer (asks for sudo in the terminal).
func (a *aiManager) installOllamaScript(ctx context.Context) (string, error) {
	if _, err := lookPath("curl"); err != nil {
		return "", errors.New("install `zstd` or `curl`, or install Ollama from https://ollama.com")
	}
	a.set(phaseInstalling, "Installing the AI helper with Ollama's official installer — check the terminal window, it may ask for your password…", -1)
	cmd := execCommandContext(ctx, "sh", "-c", "curl -fsSL https://ollama.com/install.sh | sh")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	if bin := findOllama(); bin != "" {
		return bin, nil
	}
	return "", errors.New("installer finished but ollama wasn't found")
}

type progressWriter struct {
	a           *aiManager
	label       string
	done, total int64
	last        time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if time.Since(p.last) > 400*time.Millisecond {
		p.last = time.Now()
		p.report()
	}
	return len(b), nil
}

func (p *progressWriter) report() {
	frac := -1.0
	msg := fmt.Sprintf("%s… %s", p.label, mb(p.done))
	if p.total > 0 {
		frac = float64(p.done) / float64(p.total)
		msg = fmt.Sprintf("%s… %d%% (%s of %s)", p.label, int(frac*100), mb(p.done), mb(p.total))
	}
	p.a.set(phaseInstalling, msg, frac)
	fmt.Printf("\r%s   ", msg)
}

func mb(n int64) string { return fmt.Sprintf("%.0f MB", float64(n)/1e6) }

// download streams src into dst, verifying its SHA-256, and returns the number of bytes written.
func (a *aiManager) download(ctx context.Context, src string, dst io.Writer, wantSHA, label string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", appName+"/"+version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download %s: %s", src, resp.Status)
	}
	h := newSHA()
	pw := &progressWriter{a: a, label: label, total: resp.ContentLength}
	n, err := io.Copy(io.MultiWriter(dst, h, pw), resp.Body)
	if err != nil {
		return n, err
	}
	pw.report()
	fmt.Println()
	if got := fmt.Sprintf("%x", h.Sum(nil)); got != wantSHA {
		return n, fmt.Errorf("checksum mismatch for %s", src)
	}
	return n, nil
}

func safeJoin(dest, name string) (string, error) {
	p := filepath.Join(dest, filepath.FromSlash(name))
	if !strings.HasPrefix(p, filepath.Clean(dest)+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}
	return p, nil
}

func untar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p, err := safeJoin(dest, h.Name)
		if err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := writeStream(p, tr, os.FileMode(h.Mode)|0o600); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if filepath.IsAbs(h.Linkname) || strings.Contains(h.Linkname, "..") {
				continue
			}
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.Symlink(h.Linkname, p)
		}
	}
}

func unzip(r io.ReaderAt, size int64, dest string) error {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return err
	}
	for _, f := range zr.File {
		p, err := safeJoin(dest, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeStream(p, rc, f.Mode()|0o600)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeStream(p string, r io.Reader, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Model choice mirrors the in-app guide: small machines get small models.
func modelForRAM(gb int) string {
	switch {
	case gb > 0 && gb < 7:
		return "qwen3:1.7b"
	case gb > 0 && gb < 14:
		return "llama3.2"
	case gb >= 14:
		return "gemma3:4b"
	default:
		return "llama3.2"
	}
}

// Models we know give sensible exam feedback; any already downloaded is reused instead of pulling another.
var goodModels = []string{"gemma3:4b", "llama3.2", "phi4-mini", "qwen3:4b", "qwen3:1.7b", "llama3.1", "gemma3", "mistral", "qwen2.5"}

func baseName(m string) string { return strings.TrimSuffix(m, ":latest") }

func (a *aiManager) ensureModel(ctx context.Context) (string, error) {
	ram := totalRAMGB()
	a.mu.Lock()
	a.st.RAMGB = ram
	a.mu.Unlock()

	want := a.wantModel
	if want == "" {
		want = envOr("GCSE_REVISION_MODEL", "")
	}
	have, err := a.listModels(ctx)
	if err != nil {
		return "", err
	}
	if want == "" {
		for _, g := range goodModels {
			for _, h := range have {
				if baseName(h) == g {
					return h, nil
				}
			}
		}
		want = modelForRAM(ram)
	} else {
		for _, h := range have {
			if baseName(h) == baseName(want) {
				return h, nil
			}
		}
	}
	if !a.online {
		return "", fmt.Errorf("no AI model downloaded yet, and the app was started with --offline (wanted %s)", want)
	}
	return want, a.pull(ctx, want)
}

func (a *aiManager) listModels(ctx context.Context) ([]string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.api("/api/tags"), nil)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, err
	}
	var out []string
	for _, m := range body.Models {
		if !strings.Contains(m.Name, "embed") {
			out = append(out, m.Name)
		}
	}
	return out, nil
}

func (a *aiManager) pull(ctx context.Context, model string) error {
	a.set(phasePulling, "Downloading the AI model "+model+" (one-time, a few GB)…", -1)
	body, _ := json.Marshal(map[string]any{"model": model, "stream": true})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.api("/api/pull"), strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	sc := bufio.NewScanner(r.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	last := time.Time{}
	for sc.Scan() {
		var ev struct {
			Status    string `json:"status"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if ev.Error != "" {
			return fmt.Errorf("downloading %s failed: %s", model, ev.Error)
		}
		if ev.Total > 0 && time.Since(last) > 400*time.Millisecond {
			last = time.Now()
			frac := float64(ev.Completed) / float64(ev.Total)
			msg := fmt.Sprintf("Downloading the AI model %s… %d%% (%s of %s)", model, int(frac*100), mb(ev.Completed), mb(ev.Total))
			a.set(phasePulling, msg, frac)
			fmt.Printf("\r%s   ", msg)
		}
		if ev.Status == "success" {
			fmt.Println()
			return nil
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return fmt.Errorf("downloading %s ended unexpectedly", model)
}
