package main

import "testing"

func TestEmbeddedManifestIsValidAndComplete(t *testing.T) {
	m := embeddedManifest()
	if len(m.Subjects) == 0 {
		t.Fatal("embedded subjects.js lists no subjects (or failed to parse)")
	}
	for _, e := range m.Subjects {
		if _, ok := packBytes(e); !ok {
			t.Errorf("%s: missing, or sha256 doesn't match — run `go run . manifest`", e.File)
		}
	}
}

func TestParseManifestRoundTrip(t *testing.T) {
	in := manifest{Schema: 1, Subjects: []subjectEntry{{ID: "x", File: "data/x.js", SHA256: "ab"}}}
	out, err := parseManifest(renderManifest(in))
	if err != nil || len(out.Subjects) != 1 || out.Subjects[0].ID != "x" {
		t.Fatalf("round trip failed: %v %+v", err, out)
	}
	if _, err := parseManifest([]byte("alert(1);window.REVISION_MANIFEST={}")); err == nil {
		t.Fatal("expected code before the manifest to be rejected")
	}
}

func TestPackPathValidation(t *testing.T) {
	for _, bad := range []string{"data/../x.js", "/etc/passwd", "data/a/b.js", "data/x.html"} {
		if validPackFile.MatchString(bad) {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
