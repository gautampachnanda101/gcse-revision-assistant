package main

import (
	"crypto/aes"
	"crypto/cipher"
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Packs in ./private/ are baked into the binary. Plaintext *.js are gitignored and only exist on the author's
// machine; *.js.enc (AES-256-GCM, nonce||ciphertext) are committed and decrypted at start-up with privatePackKey.
// The key ships inside the binary, so this keeps the pack out of git and out of casual view; it isn't DRM.
//
//go:embed all:private
var privateEmbedded embed.FS

// Set at build time: go build -ldflags "-X main.privatePackKey=<base64 32-byte key>". GCSE_PRIVATE_KEY overrides it
// at run time (used by `go run . encrypt-private` and development).
var privatePackKey = ""

func privateKeyBytes() ([]byte, error) {
	k := os.Getenv("GCSE_PRIVATE_KEY")
	if k == "" {
		k = privatePackKey
	}
	if k == "" {
		return nil, errors.New("no private pack key (set GCSE_PRIVATE_KEY, or build with -X main.privatePackKey=…)")
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k))
	if err != nil || len(b) != 32 {
		return nil, errors.New("private pack key must be base64 of exactly 32 bytes (openssl rand -base64 32)")
	}
	return b, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func encryptPack(key, plain []byte) ([]byte, error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := io.ReadFull(randReader, nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plain, nil), nil
}

func decryptPack(key, blob []byte) ([]byte, error) {
	g, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(blob) < g.NonceSize() {
		return nil, errors.New("encrypted pack too short")
	}
	return g.Open(nil, blob[:g.NonceSize()], blob[g.NonceSize():], nil)
}

// embeddedPrivatePacks returns baked-in packs by file name (e.g. "hinduism.js"), decrypting *.js.enc.
func embeddedPrivatePacks() map[string][]byte { return privatePacksFrom(privateEmbedded) }

// privatePacksFrom reads private/*.js and decrypts private/*.js.enc from fsys.
func privatePacksFrom(fsys fs.FS) map[string][]byte {
	out := map[string][]byte{}
	names, _ := fs.Glob(fsys, "private/*")
	var key []byte
	for _, n := range names {
		b, err := fs.ReadFile(fsys, n)
		if err != nil {
			continue
		}
		switch {
		case strings.HasSuffix(n, ".js"):
			out[path.Base(n)] = b
		case strings.HasSuffix(n, ".js.enc"):
			if key == nil {
				if key, err = privateKeyBytes(); err != nil {
					fmt.Fprintln(os.Stderr, "warning: skipping encrypted private packs:", err)
					return out
				}
			}
			plain, err := decryptPack(key, b)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: can't decrypt %s (wrong key?)\n", n)
				continue
			}
			out[strings.TrimSuffix(path.Base(n), ".enc")] = plain
		}
	}
	return out
}

// encryptPrivate encrypts every private/*.js into private/*.js.enc (the committed form).
func encryptPrivate(dir string) error {
	key, err := privateKeyBytes()
	if err != nil {
		return err
	}
	files, _ := filepath.Glob(filepath.Join(dir, "private", "*.js"))
	if len(files) == 0 {
		return errors.New("no private/*.js files to encrypt")
	}
	for _, f := range files {
		plain, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		blob, err := encryptPack(key, plain)
		if err != nil {
			return err
		}
		if err := os.WriteFile(f+".enc", blob, 0o644); err != nil {
			return err
		}
		fmt.Println("Encrypted", f+".enc")
	}
	return nil
}
