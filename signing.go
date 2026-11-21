package main

// Content signing. Published subject content lives on the repo's `content` branch: subjects.js (which lists
// every pack's SHA-256) plus subjects.js.sig, an ed25519 signature over subjects.js made in CI with a key held
// only as a GitHub Actions secret. The app accepts downloaded content only if that signature verifies against
// the public key compiled in below — so content can be published at any time without a new app release, but
// nobody without the key (not even someone who tampers with the branch) can push content to users.

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// contentPubKey is the base64 ed25519 public key for subject content. Overridable at build time
// (-ldflags "-X main.contentPubKey=…") so end-to-end tests can use a throwaway key.
var contentPubKey = "EtUMja/iqP8zvL1mn6zWIgke8LSrsy0H2gm5dgNcGRE="

const sigFile = "subjects.js.sig"

func verifyManifestSig(manifestBytes, sig []byte) error {
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(contentPubKey))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("this build has no valid content signing key")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || !ed25519.Verify(ed25519.PublicKey(pub), manifestBytes, raw) {
		return errors.New("subjects.js signature is invalid — refusing downloaded content")
	}
	return nil
}

func loadPrivateKey() (ed25519.PrivateKey, error) {
	s := strings.TrimSpace(os.Getenv("CONTENT_SIGNING_KEY"))
	if s == "" {
		return nil, errors.New("set CONTENT_SIGNING_KEY to the base64 ed25519 private key")
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) != ed25519.PrivateKeySize {
		return nil, errors.New("CONTENT_SIGNING_KEY is not a base64 ed25519 private key")
	}
	return ed25519.PrivateKey(b), nil
}

// signManifest writes <dir>/subjects.js.sig. Refuses if subjects.js doesn't match the packs on disk.
func signManifest(dir string) error {
	key, err := loadPrivateKey()
	if err != nil {
		return err
	}
	if err := checkManifestMatchesFiles(dir); err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(dir, "subjects.js"))
	if err != nil {
		return err
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(key, b))
	if err := os.WriteFile(filepath.Join(dir, sigFile), []byte(sig+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Printf("Signed %s (public key %s)\n", filepath.Join(dir, "subjects.js"),
		base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)))
	return nil
}

// verifyDir checks <dir>/subjects.js against its signature (using this build's public key) and every pack's hash.
func verifyDir(dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, "subjects.js"))
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(filepath.Join(dir, sigFile))
	if err != nil {
		return err
	}
	if err := verifyManifestSig(b, sig); err != nil {
		return err
	}
	if err := checkManifestMatchesFiles(dir); err != nil {
		return err
	}
	fmt.Println("Signature and all pack checksums OK.")
	return nil
}

func checkManifestMatchesFiles(dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, "subjects.js"))
	if err != nil {
		return err
	}
	m, err := parseManifest(b)
	if err != nil {
		return err
	}
	for _, e := range m.Subjects {
		if !validPackFile.MatchString(e.File) {
			return fmt.Errorf("invalid pack path %q", e.File)
		}
		pb, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(e.File)))
		if err != nil {
			return err
		}
		if sha256Hex(pb) != e.SHA256 {
			return fmt.Errorf("%s doesn't match its checksum in subjects.js — run `gcse-revision manifest`", e.File)
		}
	}
	return nil
}

func keygen() error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	fmt.Println("PUBLIC (put in signing.go contentPubKey):", base64.StdEncoding.EncodeToString(pub))
	fmt.Println("PRIVATE (store as the CONTENT_SIGNING_KEY secret; never commit):", base64.StdEncoding.EncodeToString(priv))
	return nil
}
