package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Optional recordings travel inside a pack folder/zip as audio/<file>; a question refers to one by bare file name.
// They are installed next to private packs and served from /audio/<pack-id>/<file> — never embedded or published.
var (
	validAudioName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.(mp3|ogg|oga|wav|m4a|webm)$`)
	validAudioID   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	audioTypes     = map[string]string{".mp3": "audio/mpeg", ".ogg": "audio/ogg", ".oga": "audio/ogg", ".wav": "audio/wav", ".m4a": "audio/mp4", ".webm": "audio/webm"}
)

const maxAudioBytes = 64 << 20

type packAudio map[string][]byte

func readAudio(fsys fs.FS) (packAudio, error) {
	root, err := packRoot(fsys)
	if err != nil {
		return nil, err
	}
	files, _ := fs.Glob(root, "audio/*")
	out := packAudio{}
	var total int
	for _, f := range files {
		name := path.Base(f)
		if !validAudioName.MatchString(name) {
			return nil, fmt.Errorf("%s: audio file names must be letters, digits, . _ - and end in mp3, ogg, wav, m4a or webm", f)
		}
		b, err := readLimited(root, f)
		if err != nil {
			return nil, err
		}
		if total += len(b); total > maxAudioBytes {
			return nil, errors.New("audio files are too large (64 MB total limit)")
		}
		out[name] = b
	}
	return out, nil
}

func checkAudioRefs(p packFile, a packAudio) error {
	var missing []string
	for _, qs := range p.Questions {
		for _, q := range qs {
			if q.Audio == "" {
				continue
			}
			if _, ok := a[q.Audio]; !ok {
				missing = append(missing, q.Audio)
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("questions refer to audio files that are not in audio/: %s", strings.Join(missing, ", "))
	}
	return nil
}

func hasAudioRefs(p packFile) bool {
	for _, qs := range p.Questions {
		for _, q := range qs {
			if q.Audio != "" {
				return true
			}
		}
	}
	return false
}

func audioDir(id string) string { return filepath.Join(privatePacksDir(), "audio", id) }

func stageAudio(root, id string, a packAudio) (string, error) {
	if !validAudioID.MatchString(id) {
		return "", fmt.Errorf("pack id %q is not valid", id)
	}
	dir := filepath.Join(root, "audio")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for name, b := range a {
		if err := writeFileAtomic(filepath.Join(dir, name), b); err != nil {
			return "", err
		}
	}
	return dir, nil
}

func serveAudio(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/audio/"), "/")
	if len(parts) != 2 || !validAudioID.MatchString(parts[0]) || !validAudioName.MatchString(parts[1]) {
		http.NotFound(w, r)
		return
	}
	b, err := os.ReadFile(filepath.Join(audioDir(parts[0]), parts[1]))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", audioTypes[strings.ToLower(filepath.Ext(parts[1]))])
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}
