// gcse-revision: a single binary that serves the GCSE revision app on localhost,
// keeps subject packs up to date from the public repo (when online), and sets up a local AI helper
// (Ollama + a model sized to this machine). Everything keeps working offline once installed.
package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Set at build time: go build -ldflags "-X main.version=v1.2.3 -X main.repo=owner/name"
var (
	version = "dev"
	repo    = "gautampachnanda101/gcse-revision-assistant" // GitHub repo hosting subjects.js + data/*.js
)

const appName = "gcse-revision"

//go:embed app.html subjects.js data/*.js
var embedded embed.FS

func usage() {
	writeUsage(os.Stderr)
}

type options struct {
	port      int
	noBrowser bool
	offline   bool
	noAI      bool
	model     string
	repo      string
	branch    string
	service   bool
	noAutoUp  bool
}

func main() {
	exitProcess(runCommandLine(os.Args[1:]))
}

// newFlagSet declares the global flags, bound to opts.
func newFlagSet(opts *options) *flag.FlagSet {
	fl := flag.NewFlagSet(appName, flag.ContinueOnError)
	fl.IntVar(&opts.port, "port", 8765, "port to serve on (127.0.0.1 only)")
	fl.BoolVar(&opts.noBrowser, "no-browser", false, "don't open a browser window")
	fl.BoolVar(&opts.service, "service", false, "run as a managed background service")
	fl.BoolVar(&opts.offline, "offline", false, "never contact the internet (no subject updates, no AI downloads)")
	fl.BoolVar(&opts.noAI, "no-ai", false, "don't install/start the local AI helper")
	fl.BoolVar(&opts.noAutoUp, "no-auto-update", false, "disable automatic application upgrades")
	fl.StringVar(&opts.model, "model", "", "Ollama model to use (default: chosen from this machine's RAM)")
	fl.StringVar(&opts.repo, "repo", envOr("GCSE_REVISION_REPO", repo), "GitHub owner/name to fetch subject packs from")
	fl.StringVar(&opts.branch, "branch", envOr("GCSE_REVISION_BRANCH", "content"), "legacy content branch used only by test/mirror endpoints")
	fl.Usage = usage
	return fl
}

// runCommandLine executes one command line (without the program name) and returns the process exit code:
// 0 on success, 1 when the command fails, 2 for usage errors.
func runCommandLine(argv []string) int {
	opts := options{}
	fl := newFlagSet(&opts)
	// Accept flags before or after the subcommand: `gcse-revision update --offline` and `gcse-revision --offline update`.
	cmd := ""
	args := argv
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if cmd == "" && fl.NArg() > 0 {
		cmd = fl.Arg(0)
		if err := fl.Parse(fl.Args()[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
	}
	var err error
	switch cmd {
	case "", "serve", "run":
		err = serve(opts)
	case "service":
		err = serviceCommand(fl.Args(), opts)
	case "upgrade":
		err = upgradeCommand(context.Background())
	case "doctor":
		err = runDoctor(opts)
	case "completion":
		err = writeCompletion(os.Stdout, fl.Arg(0))
	case "update":
		var n int
		n, err = syncSubjects(context.Background(), opts.repo, opts.branch, logf)
		if err == nil {
			logf("Subject packs up to date (%d downloaded).", n)
		}
	case "setup-ai":
		ai := newAIManager(opts.model, !opts.offline)
		ai.ensure(context.Background())
		ai.shutdown() // the app starts the helper itself whenever it runs
		st := ai.status()
		if st.Phase != phaseReady {
			err = errors.New(st.Message)
		}
	case "build":
		if fl.NArg() == 0 {
			err = errors.New("usage: gcse-revision build content/<pack-id> [content/<pack-id> …]")
		}
		for _, d := range fl.Args() {
			if err = buildPack(d, "."); err != nil {
				break
			}
		}
		if err == nil {
			err = writeManifest(".")
		}
	case "sign":
		err = signManifest(orDefault(fl.Arg(0), "."))
	case "verify":
		err = verifyDir(orDefault(fl.Arg(0), "."))
	case "import":
		err = importPaths(fl.Args())
	case "export":
		if fl.NArg() < 1 {
			err = errors.New("usage: gcse-revision export <data-pack.js> [out.json]")
			break
		}
		dst := fl.Arg(1)
		if dst == "" {
			dst = strings.TrimSuffix(fl.Arg(0), ".js") + ".pack.json"
		}
		if err = exportPack(fl.Arg(0), dst); err == nil {
			fmt.Println("Wrote", dst)
		}
	case "encrypt-private":
		err = encryptPrivate(".")
	case "keygen":
		err = keygen()
	case "validate":
		err = validateFiles(fl.Args())
	case "manifest":
		dir := fl.Arg(0)
		if dir == "" {
			dir = "."
		}
		err = writeManifest(dir)
	case "version", "--version":
		fmt.Println(appName, version)
	case "help", "--help", "-h":
		if fl.Arg(0) == "" {
			usage()
		} else {
			err = helpTopic(os.Stdout, fl.Arg(0))
		}
	default:
		usage()
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func logf(format string, args ...any) {
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	fmt.Print(line)
	if serviceLog != nil {
		_, _ = serviceLog.WriteString(line)
	}
}

// dataDir is where downloaded subject packs, the private Ollama copy and logs live.
func dataDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, appName)
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func serve(opts options) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveContext(ctx, opts)
}

// serveContext runs the app until ctx is cancelled.
func serveContext(ctx context.Context, opts options) error {
	if opts.service {
		if err := enableServiceLogging(); err != nil {
			return err
		}
		defer closeServiceLogging()
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(opts.port))
	ln, err := listen("tcp", addr)
	if err != nil {
		// Already running? Just open the existing instance.
		if alreadyRunning(addr) {
			url := browserURL(opts.port)
			logf("Already running at %s — opening it.", url)
			if !opts.noBrowser {
				openBrowser(url)
			}
			return nil
		}
		if opts.service {
			return fmt.Errorf("configured service port %d is already in use", opts.port)
		}
		logf("Port %d is busy; picking a free one.", opts.port)
		if ln, err = listen("tcp", "127.0.0.1:0"); err != nil {
			return err
		}
	}
	defer ln.Close()
	activePort := opts.port
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		activePort = tcp.Port
	}
	url := browserURL(activePort)

	if !opts.offline && opts.repo != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if n, err := syncSubjects(ctx, opts.repo, opts.branch, logf); err != nil {
			logf("Subject update skipped (%v) — using the subjects already on this computer.", err)
		} else if n > 0 {
			logf("Downloaded %d new/updated subject pack(s).", n)
		}
		cancel()
	}

	ai := newAIManager(opts.model, !opts.offline)
	if opts.noAI {
		ai.disable()
	}
	cloudAI, err := newCloudAIManager()
	if err != nil {
		return err
	}
	localToken, err := newLocalAuthToken()
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: newHandler(ln.Addr().String(), ai, cloudAI, localToken), ReadHeaderTimeout: 10 * time.Second}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if !opts.noAI {
		go ai.ensure(ctx)
	}
	if !opts.noAutoUp {
		automaticUpgradeLoop(ctx, opts)
	}

	logf("GCSE Revision %s is running at %s", version, url)
	if opts.service {
		logf("Background service ready; run '%s service status' for details.", appName)
	} else {
		logf("Keep this window open while you revise. Press Ctrl+C to quit.")
	}
	if !opts.noBrowser {
		openBrowser(url)
	}
	err = srv.Serve(ln)
	ai.shutdown()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func alreadyRunning(addr string) bool {
	c := http.Client{Timeout: 1500 * time.Millisecond}
	r, err := c.Get("http://" + addr + "/api/info")
	if err != nil {
		return false
	}

	defer r.Body.Close()
	var info map[string]any
	return json.NewDecoder(r.Body).Decode(&info) == nil && info["app"] == appName
}

func browserURL(port int) string {
	return "http://gcse-revision.localhost:" + strconv.Itoa(port) + "/"
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch hostOS {
	case "darwin":
		cmd = execCommand("open", url)
	case "windows":
		cmd = execCommand("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = execCommand("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		logf("Open %s in your browser.", url)
		return
	}
	go cmd.Wait()
}

// newHandler serves the embedded app + subject packs and the local AI endpoints.
// It only answers requests addressed to this exact host (blocks DNS-rebinding from other sites).
func newHandler(host string, ai *aiManager, cloudAI *cloudAIManager, localToken string) http.Handler {
	mux := http.NewServeMux()
	staticFS, _ := fs.Sub(embedded, ".")

	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"app": appName, "version": version})
	})
	mux.HandleFunc("/api/llm/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, ai.status())
	})
	mux.HandleFunc("/api/llm/builtin", func(w http.ResponseWriter, r *http.Request) {
		available, detail := appleAIAvailable()
		writeJSON(w, map[string]any{"available": available, "provider": detail, "kind": "apple-foundation-models"})
	})
	mux.HandleFunc("/llm/builtin/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			Messages []chatMessage `json:"messages"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		text, err := chatWithAppleAI(request.Messages)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": text}}},
		})
	})
	mux.HandleFunc("/api/llm/setup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		ai.enable()
		go ai.ensure(context.Background())
		writeJSON(w, ai.status())
	})
	mux.HandleFunc("/api/cloud-ai/auth", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !hasLocalAuth(r, localToken) {
			writeCloudAIError(w, http.StatusUnauthorized, errors.New("local session authentication required"))
			return
		}
		switch r.Method {
		case http.MethodGet:
			configured, err := cloudAI.auth.configured()
			if err != nil {
				writeCloudAIError(w, http.StatusServiceUnavailable, err)
				return
			}
			writeJSON(w, map[string]bool{"configured": configured})
		case http.MethodPost:
			var request struct {
				Password     string `json:"password"`
				Confirmation string `json:"confirmation"`
			}
			if err := decodeCloudJSON(w, r, 16<<10, &request); err != nil {
				writeCloudAIError(w, http.StatusBadRequest, errors.New("invalid authentication request"))
				return
			}
			token, err := cloudAI.auth.login(request.Password, request.Confirmation)
			if err != nil {
				writeCloudAIError(w, http.StatusUnauthorized, err)
				return
			}
			writeJSON(w, map[string]any{"authenticated": true, "sessionToken": token})
		case http.MethodDelete:
			cloudAI.auth.revoke(r.Header.Get("X-Cloud-AI-Session"))
			writeJSON(w, map[string]bool{"authenticated": false})
		default:
			w.Header().Set("Allow", "GET, POST, DELETE")
			writeCloudAIError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		}
	})
	mux.HandleFunc("/api/cloud-ai/config", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !hasLocalAuth(r, localToken) || !cloudAI.auth.authorize(r.Header.Get("X-Cloud-AI-Session")) {
			writeCloudAIError(w, http.StatusUnauthorized, errors.New("grown-up sign-in required"))
			return
		}
		switch r.Method {
		case http.MethodGet:
			status, err := cloudAI.status()
			if err != nil {
				writeCloudAIError(w, http.StatusServiceUnavailable, err)
				return
			}
			writeJSON(w, status)
		case http.MethodPost:
			var request struct {
				Endpoint string `json:"endpoint"`
				Model    string `json:"model"`
				APIKey   string `json:"apiKey"`
			}
			if err := decodeCloudJSON(w, r, 16<<10, &request); err != nil {
				writeCloudAIError(w, http.StatusBadRequest, errors.New("invalid cloud AI settings"))
				return
			}
			if err := cloudAI.save(cloudAIConfig{Endpoint: request.Endpoint, Model: request.Model}, request.APIKey); err != nil {
				writeCloudAIError(w, http.StatusBadRequest, err)
				return
			}
			status, err := cloudAI.status()
			if err != nil {
				writeCloudAIError(w, http.StatusServiceUnavailable, err)
				return
			}
			writeJSON(w, status)
		case http.MethodDelete:
			if err := cloudAI.clear(); err != nil {
				writeCloudAIError(w, http.StatusInternalServerError, err)
				return
			}
			writeJSON(w, map[string]bool{"configured": false})
		default:
			w.Header().Set("Allow", "GET, POST, DELETE")
			writeCloudAIError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		}
	})
	mux.HandleFunc("/api/cloud-ai/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !hasLocalAuth(r, localToken) || !cloudAI.auth.authorize(r.Header.Get("X-Cloud-AI-Session")) {
			writeCloudAIError(w, http.StatusUnauthorized, errors.New("grown-up sign-in required"))
			return
		}
		cloudAI.chat(w, r)
	})
	mux.Handle("/llm/", ai.proxy())
	mux.HandleFunc("/api/import", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBytes))
		if err == nil {
			var res importResult
			if res, err = importBytes(b); err == nil {
				writeJSON(w, res)
				return
			}
		}
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": err.Error()})
	})

	mux.HandleFunc("/subjects.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(renderManifest(effectiveManifest()))
	})
	mux.HandleFunc("/audio/", serveAudio)
	mux.HandleFunc("/data/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		b, err := readSubjectFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/app.html" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		b, _ := fs.ReadFile(staticFS, "app.html")
		b = bytes.ReplaceAll(b, []byte("__GCSE_REVISION_LOCAL_TOKEN__"), []byte(localToken))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Host
		allowed := h == host ||
			h == strings.Replace(host, "127.0.0.1", "localhost", 1) ||
			h == strings.Replace(host, "127.0.0.1", "gcse-revision.localhost", 1)
		if !allowed {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		// State-changing requests must come from our own page.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" && o != "http://"+h {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
			http.Error(w, "forbidden fetch site", http.StatusForbidden)
			return
		}
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		mux.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}
