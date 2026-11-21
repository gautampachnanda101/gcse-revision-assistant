package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestBrowserURLUsesLocalhostReservation(t *testing.T) {
	if got := browserURL(8765); got != "http://gcse-revision.localhost:8765/" {
		t.Fatalf("browserURL = %q", got)
	}
}

func TestHandlerAcceptsLocalhostSubdomain(t *testing.T) {
	h := newHandler("127.0.0.1:8765", newAIManager("", false))
	req := httptest.NewRequest(http.MethodGet, "http://gcse-revision.localhost:8765/api/info", nil)
	req.Host = "gcse-revision.localhost:8765"
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestUsageIsUsefulAndColorSafe(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var b bytes.Buffer
	writeUsage(&b)
	for _, want := range []string{"service status", "upgrade", "gcse-revision.localhost", "NO_COLOR"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("help is missing %q", want)
		}
	}
	if strings.Contains(b.String(), "\x1b[") {
		t.Fatal("NO_COLOR help contains ANSI escapes")
	}
}

func TestPackageManagedPaths(t *testing.T) {
	for _, path := range []string{
		"/opt/homebrew/Cellar/gcse-revision/1.0/bin/gcse-revision",
		`C:\Users\me\scoop\apps\gcse-revision\current\gcse-revision.exe`,
	} {
		if !packageManagedPath(path) {
			t.Fatalf("package-managed path not detected: %s", path)
		}
	}
	if packageManagedPath(filepathForTest()) {
		t.Fatal("temporary standalone path detected as package-managed")
	}
}

func filepathForTest() string {
	return strings.ReplaceAll(os.TempDir(), `\`, "/") + "/gcse-revision"
}

func TestServeExistingInstanceDoesNotDereferenceNilListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"app":"gcse-revision"}`))
	})}
	go srv.Serve(ln)
	defer srv.Close()
	_, rawPort, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(rawPort)
	if err := serve(options{port: port, noBrowser: true, offline: true, noAI: true, noAutoUp: true}); err != nil {
		t.Fatal(err)
	}
}

func TestManagedServiceRejectsBusyPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"app":"something-else"}`))
	})}
	go srv.Serve(ln)
	defer srv.Close()
	_, rawPort, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(rawPort)
	err = serve(options{port: port, noBrowser: true, offline: true, noAI: true, noAutoUp: true, service: true})
	if err == nil || !strings.Contains(err.Error(), "service port") {
		t.Fatalf("service busy-port error = %v", err)
	}
}
