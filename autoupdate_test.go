package main

import (
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"v0.1.1", "v0.1.0", 1},
		{"v0.1.0", "v0.1.1", -1},
		{"v1.0.0", "v1.0.0", 0},
		{"v2.0.0", "v1.99.99", 1},
	} {
		got, err := compareVersions(tc.a, tc.b)
		if err != nil || got != tc.want {
			t.Fatalf("compareVersions(%q, %q) = %d, %v; want %d", tc.a, tc.b, got, err, tc.want)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	checksums := []byte("abc123  first\n" +
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  gcse-revision-darwin-arm64\n")
	got, err := checksumFor(checksums, "gcse-revision-darwin-arm64")
	if err != nil || got != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("checksumFor = %q, %v", got, err)
	}
}

func TestAIPanelIsCollapsible(t *testing.T) {
	b, err := readEmbeddedText("app.html")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`<details class="local-ai-panel"`, `Optional · machine-generated answers may be wrong`} {
		if !contains(s, want) {
			t.Fatalf("app.html is missing %q", want)
		}
	}
}

func TestAuthorStarterIsImportable(t *testing.T) {
	b, err := readEmbeddedText("app.html")
	if err != nil {
		t.Fatal(err)
	}
	const marker = `<pre class="code-sample" id="author-starter">`
	s := string(b)
	start := strings.Index(s, marker)
	if start < 0 {
		t.Fatal("author starter is missing")
	}
	start += len(marker)
	end := strings.Index(s[start:], "</pre>")
	if end < 0 {
		t.Fatal("author starter closing tag is missing")
	}
	raw := []byte(s[start : start+end])
	_, _, _, err = loadPackBytes(raw)
	if err != nil {
		t.Fatalf("author starter is not importable: %v", err)
	}
}

func readEmbeddedText(name string) ([]byte, error) {
	return embedded.ReadFile(name)
}

func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
