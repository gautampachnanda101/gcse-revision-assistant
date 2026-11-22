package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func testKey(t *testing.T) ([]byte, string) {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k, base64.StdEncoding.EncodeToString(k)
}

func TestPrivateKeyBytes(t *testing.T) {
	isolateHome(t)
	setVar(t, &privatePackKey, "")
	if _, err := privateKeyBytes(); err == nil {
		t.Fatal("expected an error when no key is configured")
	}

	t.Setenv("GCSE_PRIVATE_KEY", "not base64!")
	errContains(t, func() error { _, err := privateKeyBytes(); return err }(), "exactly 32 bytes")

	t.Setenv("GCSE_PRIVATE_KEY", base64.StdEncoding.EncodeToString([]byte("short")))
	errContains(t, func() error { _, err := privateKeyBytes(); return err }(), "exactly 32 bytes")

	raw, enc := testKey(t)
	t.Setenv("GCSE_PRIVATE_KEY", " "+enc+"\n")
	got, err := privateKeyBytes()
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("env key: got %x, %v", got, err)
	}

	// The build-time key is used when the environment doesn't override it.
	t.Setenv("GCSE_PRIVATE_KEY", "")
	raw2, enc2 := testKey(t)
	setVar(t, &privatePackKey, enc2)
	if got, err := privateKeyBytes(); err != nil || !bytes.Equal(got, raw2) {
		t.Fatalf("build key: got %x, %v", got, err)
	}
}

func TestEncryptDecryptPack(t *testing.T) {
	key, _ := testKey(t)
	plain := []byte("window.REVISION_SUBJECTS.push({})")
	blob, err := encryptPack(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, plain) {
		t.Fatal("ciphertext contains the plaintext")
	}
	again, _ := encryptPack(key, plain)
	if bytes.Equal(blob, again) {
		t.Fatal("two encryptions share a nonce")
	}
	got, err := decryptPack(key, blob)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("round trip = %q, %v", got, err)
	}

	other, _ := testKey(t)
	if _, err := decryptPack(other, blob); err == nil {
		t.Fatal("decrypting with the wrong key should fail")
	}
	blob[len(blob)-1] ^= 0xff
	if _, err := decryptPack(key, blob); err == nil {
		t.Fatal("tampered ciphertext should fail authentication")
	}
	errContains(t, func() error { _, err := decryptPack(key, []byte("tiny")); return err }(), "too short")
	if _, err := encryptPack([]byte("bad key"), plain); err == nil {
		t.Fatal("invalid AES key length should fail")
	}
	if _, err := decryptPack([]byte("bad key"), blob); err == nil {
		t.Fatal("invalid AES key length should fail")
	}
}

func TestPrivatePacksFrom(t *testing.T) {
	isolateHome(t)
	setVar(t, &privatePackKey, "")
	key, enc := testKey(t)
	sealed, err := encryptPack(key, []byte("secret pack"))
	if err != nil {
		t.Fatal(err)
	}
	otherKey, _ := testKey(t)
	wrong, _ := encryptPack(otherKey, []byte("unreadable"))
	fsys := fstest.MapFS{
		"private/plain.js":      {Data: []byte("plain pack")},
		"private/sealed.js.enc": {Data: sealed},
		"private/wrong.js.enc":  {Data: wrong},
		"private/README.md":     {Data: []byte("ignored")},
		"private/dir.js":        {Mode: os.ModeDir},
	}

	// Without a key, plaintext packs still load and encrypted ones are skipped with a warning.
	got := privatePacksFrom(fsys)
	if string(got["plain.js"]) != "plain pack" || got["sealed.js"] != nil {
		t.Fatalf("no-key result = %v", got)
	}

	t.Setenv("GCSE_PRIVATE_KEY", enc)
	got = privatePacksFrom(fsys)
	if string(got["sealed.js"]) != "secret pack" {
		t.Fatalf("decrypted pack = %q", got["sealed.js"])
	}
	if _, ok := got["wrong.js"]; ok {
		t.Fatal("a pack encrypted with another key must be skipped")
	}
	if _, ok := got["README.md"]; ok {
		t.Fatal("non-pack files must be ignored")
	}
	// The real embedded folder always loads without panicking.
	_ = embeddedPrivatePacks()
}

func TestEncryptPrivate(t *testing.T) {
	isolateHome(t)
	setVar(t, &privatePackKey, "")
	dir := t.TempDir()
	if err := encryptPrivate(dir); err == nil {
		t.Fatal("expected a missing-key error")
	}
	key, enc := testKey(t)
	t.Setenv("GCSE_PRIVATE_KEY", enc)
	errContains(t, encryptPrivate(dir), "no private/*.js")

	writeFile(t, filepath.Join(dir, "private", "a.js"), "pack A")
	out := captureStdout(t, func() {
		if err := encryptPrivate(dir); err != nil {
			t.Fatal(err)
		}
	})
	mustContain(t, out, "Encrypted")
	blob, err := os.ReadFile(filepath.Join(dir, "private", "a.js.enc"))
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := decryptPack(key, blob); err != nil || string(plain) != "pack A" {
		t.Fatalf("decrypt = %q, %v", plain, err)
	}

	// Write failure: the .enc target is a directory.
	if err := os.Remove(filepath.Join(dir, "private", "a.js.enc")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "private", "a.js.enc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := encryptPrivate(dir); err == nil {
		t.Fatal("expected a write error")
	}

	// Read failure: a directory whose name matches *.js.
	dir2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir2, "private", "b.js"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := encryptPrivate(dir2); err == nil || !strings.Contains(err.Error(), "b.js") {
		t.Fatalf("expected a read error, got %v", err)
	}
}
