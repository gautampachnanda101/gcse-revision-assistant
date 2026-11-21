// gcse-revision: a single, dependency-free binary that serves the GCSE revision app on localhost,
// keeps subject packs up to date from the public repo (when online), and sets up a local AI helper
// (Ollama + a model sized to this machine). Everything keeps working offline once installed.
package main

import (
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
	"runtime"
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
	fmt.Fprintf(os.Stderr, `%s %s — GCSE revision app (runs fully offline once installed)

Usage:
  %s [flags]            start the app and open it in your browser (default)
  %s update             download new/updated subject packs, then exit
  %s setup-ai           install/start the local AI helper and download its model, then exit
  %s build content/<id> (maintainers) assemble a subject pack from content/<id>/ into data/<id>.js
  %s validate [files]   (maintainers) check subject packs (default: data/*.js)
  %s manifest [dir]     (maintainers) regenerate subjects.js from <dir>/data/*.js (default ".")
  %s import <file|dir>  import a subject (bundle .json, folder or .zip in the open pack format) for this computer only
  %s export <pack.js>   convert a built pack to the open bundle format (.pack.json)
  %s encrypt-private    (maintainers) encrypt private/*.js → private/*.js.enc using GCSE_PRIVATE_KEY
  %s version            print the version

Flags:

Private packs (used only on this computer, never published): put pack .js files in
  %s
`, appName, version, appName, appName, appName, appName, appName, appName, appName, appName, appName, appName, privatePacksDir())
	flag.PrintDefaults()
}

type options struct {
	port      int
	noBrowser bool
	offline   bool
	noAI      bool
	model     string
	repo      string
	branch    string
}

func main() {
	opts := options{}
	flag.IntVar(&opts.port, "port", 8765, "port to serve on (127.0.0.1 only)")
	flag.BoolVar(&opts.noBrowser, "no-browser", false, "don't open a browser window")
	flag.BoolVar(&opts.offline, "offline", false, "never contact the internet (no subject updates, no AI downloads)")
	flag.BoolVar(&opts.noAI, "no-ai", false, "don't install/start the local AI helper")
	flag.StringVar(&opts.model, "model", "", "Ollama model to use (default: chosen from this machine's RAM)")
	flag.StringVar(&opts.repo, "repo", envOr("GCSE_REVISION_REPO", repo), "GitHub owner/name to fetch subject packs from")
	flag.StringVar(&opts.branch, "branch", envOr("GCSE_REVISION_BRANCH", "content"), "branch to fetch signed subject packs from")
	flag.Usage = usage
	// Accept flags before or after the subcommand: `gcse-revision update --offline` and `gcse-revision --offline update`.
	cmd := ""
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	_ = flag.CommandLine.Parse(args)
	if cmd == "" && flag.NArg() > 0 {
		cmd = flag.Arg(0)
		_ = flag.CommandLine.Parse(flag.Args()[1:])
	}
	var err error
	switch cmd {
	case "", "serve", "run":
		err = serve(opts)
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
		if flag.NArg() == 0 {
			err = errors.New("usage: gcse-revision build content/<pack-id> [content/<pack-id> …]")
		}
		for _, d := range flag.Args() {
			if err = buildPack(d, "."); err != nil {
				break
			}
		}
		if err == nil {
			err = writeManifest(".")
		}
	case "sign":
		err = signManifest(orDefault(flag.Arg(0), "."))
	case "verify":
		err = verifyDir(orDefault(flag.Arg(0), "."))
	case "import":
		err = importPaths(flag.Args())
	case "export":
		if flag.NArg() < 1 {
			err = errors.New("usage: gcse-revision export <data-pack.js> [out.json]")
			break
		}
		dst := flag.Arg(1)
		if dst == "" {
			dst = strings.TrimSuffix(flag.Arg(0), ".js") + ".pack.json"
		}
		if err = exportPack(flag.Arg(0), dst); err == nil {
			fmt.Println("Wrote", dst)
		}
	case "encrypt-private":
		err = encryptPrivate(".")
	case "keygen":
		err = keygen()
	case "validate":
		err = validateFiles(flag.Args())
	case "manifest":
		dir := flag.Arg(0)
		if dir == "" {
			dir = "."
		}
		err = writeManifest(dir)
	case "version", "--version":
		fmt.Println(appName, version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func logf(format string, args ...any) {
	fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
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
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(opts.port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// Already running? Just open the existing instance.
		if alreadyRunning(addr) {
			url := "http://" + addr + "/"
			logf("Already running at %s — opening it.", url)
			if !opts.noBrowser {
				openBrowser(url)
			}
			return nil
		}
		logf("Port %d is busy; picking a free one.", opts.port)
		if ln, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			return err
		}
	}
	url := "http://" + ln.Addr().String() + "/"

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
	srv := &http.Server{Handler: newHandler(ln.Addr().String(), ai), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	if !opts.noAI {
		go ai.ensure(ctx)
	}

	logf("GCSE Revision %s is running at %s", version, url)
	logf("Keep this window open while you revise. Press Ctrl+C to quit.")
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

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		logf("Open %s in your browser.", url)
		return
	}
	go cmd.Wait()
}

// newHandler serves the embedded app + subject packs and the local AI endpoints.
// It only answers requests addressed to this exact host (blocks DNS-rebinding from other sites).
func newHandler(host string, ai *aiManager) http.Handler {
	mux := http.NewServeMux()
	staticFS, _ := fs.Sub(embedded, ".")

	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"app": appName, "version": version})
	})
	mux.HandleFunc("/api/llm/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, ai.status())
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
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Host
		if h != host && h != strings.Replace(host, "127.0.0.1", "localhost", 1) {
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
