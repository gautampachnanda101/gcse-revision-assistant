package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimalPackJS is the smallest data pack that writeManifest accepts.
func minimalPackJS(storageID, title string) string {
	return `window.REVISION_SUBJECTS=window.REVISION_SUBJECTS||[];` + "\n" +
		`window.REVISION_SUBJECTS.push({"config":{"storageId":"` + storageID + `","brandTitle":"` + title +
		`","examBoard":"Board","subjectArea":"Area"}});` + "\n"
}

// contentDir builds <dir>/data/*.js plus a matching subjects.js.
func contentDir(t *testing.T, packs map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range packs {
		writeFile(t, filepath.Join(dir, "data", name), body)
	}
	captureStdout(t, func() {
		if err := writeManifest(dir); err != nil {
			t.Fatal(err)
		}
	})
	return dir
}

// withSigningKey installs a throwaway key pair: the public half as this build's contentPubKey and the
// private half as CONTENT_SIGNING_KEY.
func withSigningKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	setVar(t, &contentPubKey, base64.StdEncoding.EncodeToString(pub))
	t.Setenv("CONTENT_SIGNING_KEY", base64.StdEncoding.EncodeToString(priv))
	return priv
}

func TestVerifyManifestSig(t *testing.T) {
	priv := withSigningKey(t)
	msg := []byte("window.REVISION_MANIFEST={}")
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg))
	if err := verifyManifestSig(msg, []byte(sig+"\n")); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	errContains(t, verifyManifestSig(append(msg, ' '), []byte(sig)), "signature is invalid")
	errContains(t, verifyManifestSig(msg, []byte("%%%")), "signature is invalid")

	setVar(t, &contentPubKey, "not-a-key")
	errContains(t, verifyManifestSig(msg, []byte(sig)), "no valid content signing key")
}

func TestLoadPrivateKey(t *testing.T) {
	t.Setenv("CONTENT_SIGNING_KEY", "")
	errContains(t, func() error { _, err := loadPrivateKey(); return err }(), "set CONTENT_SIGNING_KEY")
	t.Setenv("CONTENT_SIGNING_KEY", base64.StdEncoding.EncodeToString([]byte("short")))
	errContains(t, func() error { _, err := loadPrivateKey(); return err }(), "not a base64 ed25519")
	withSigningKey(t)
	if _, err := loadPrivateKey(); err != nil {
		t.Fatal(err)
	}
}

func TestSignAndVerifyDir(t *testing.T) {
	withSigningKey(t)
	dir := contentDir(t, map[string]string{"one.js": minimalPackJS("one", "One")})

	out := captureStdout(t, func() {
		if err := signManifest(dir); err != nil {
			t.Fatal(err)
		}
	})
	mustContain(t, out, "Signed", "public key")
	out = captureStdout(t, func() {
		if err := verifyDir(dir); err != nil {
			t.Fatal(err)
		}
	})
	mustContain(t, out, "Signature and all pack checksums OK")

	// A pack edited after signing fails verification…
	writeFile(t, filepath.Join(dir, "data", "one.js"), minimalPackJS("one", "Changed"))
	errContains(t, verifyDir(dir), "doesn't match its checksum")
	// …and signing refuses until the manifest is regenerated.
	errContains(t, signManifest(dir), "doesn't match its checksum")

	// A different build key rejects the signature.
	captureStdout(t, func() { _ = writeManifest(dir); _ = signManifest(dir) })
	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	setVar(t, &contentPubKey, base64.StdEncoding.EncodeToString(pub2))
	errContains(t, verifyDir(dir), "signature is invalid")
}

func TestSignAndVerifyDirFailures(t *testing.T) {
	withSigningKey(t)
	empty := t.TempDir()
	if err := verifyDir(empty); err == nil {
		t.Fatal("missing subjects.js should fail")
	}
	if err := signManifest(empty); err == nil {
		t.Fatal("missing subjects.js should fail")
	}

	dir := contentDir(t, map[string]string{"one.js": minimalPackJS("one", "One")})
	if err := verifyDir(dir); err == nil {
		t.Fatal("missing signature should fail")
	}

	// The signature path is a directory, so writing it fails.
	if err := os.Mkdir(filepath.Join(dir, sigFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := signManifest(dir); err == nil {
		t.Fatal("expected a write failure")
	}

	t.Setenv("CONTENT_SIGNING_KEY", "")
	if err := signManifest(dir); err == nil {
		t.Fatal("missing key should fail")
	}
}

func TestCheckManifestBytes(t *testing.T) {
	dir := t.TempDir()
	if err := checkManifestBytes(dir, []byte("not a manifest")); err == nil {
		t.Fatal("garbage manifest should fail")
	}
	bad := renderManifest(manifest{Schema: 1, Subjects: []subjectEntry{{ID: "x", File: "../etc/passwd", SHA256: "00"}}})
	errContains(t, checkManifestBytes(dir, bad), "invalid pack path")
	missing := renderManifest(manifest{Schema: 1, Subjects: []subjectEntry{{ID: "x", File: "data/missing.js", SHA256: "00"}}})
	if err := checkManifestBytes(dir, missing); err == nil {
		t.Fatal("missing pack should fail")
	}
}

func TestKeygen(t *testing.T) {
	setVar(t, &randReader, io.Reader(failingReader{}))
	if err := keygen(); err == nil {
		t.Fatal("key generation without randomness should fail")
	}
	setVar(t, &randReader, rand.Reader)
	out := captureStdout(t, func() {
		if err := keygen(); err != nil {
			t.Fatal(err)
		}
	})
	var pub, priv string
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) > 0 {
			switch {
			case strings.HasPrefix(line, "PUBLIC"):
				pub = f[len(f)-1]
			case strings.HasPrefix(line, "PRIVATE"):
				priv = f[len(f)-1]
			}
		}
	}
	pb, _ := base64.StdEncoding.DecodeString(pub)
	kb, _ := base64.StdEncoding.DecodeString(priv)
	if len(pb) != ed25519.PublicKeySize || len(kb) != ed25519.PrivateKeySize {
		t.Fatalf("keygen printed malformed keys:\n%s", out)
	}
	if !ed25519.PrivateKey(kb).Public().(ed25519.PublicKey).Equal(ed25519.PublicKey(pb)) {
		t.Fatal("public and private halves don't match")
	}
}

// failingReader stands in for an exhausted entropy source.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("no entropy") }
