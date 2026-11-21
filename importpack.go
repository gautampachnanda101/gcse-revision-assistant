package main

// Open pack format and import/export.
//
// A subject is plain JSON described by schema/gcse-revision-pack.schema.json (JSON Schema 2020-12). It can be
// handed around three ways, and `gcse-revision import` accepts all of them:
//
//   - a single bundle file:  {"config":{…},"rubrics":{…},"modules":[{…},…]}
//   - a folder or .zip:      pack.json + modules/*.json  (the layout used in content/<id>/)
//   - an already built data pack (window.REVISION_SUBJECTS.push({…}))
//
// Imported packs are validated against the schema, then installed in the private packs folder: they appear in
// the subject picker on this computer only and are never uploaded or published.

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed schema/gcse-revision-pack.schema.json
var packSchemaJSON []byte

const (
	maxImportBytes = 32 << 20
	maxZipEntries  = 2000
)

func packSchema() *schemaDoc {
	s, err := parseSchema(packSchemaJSON)
	if err != nil {
		panic("embedded pack schema is invalid: " + err.Error())
	}
	return s
}

func schemaProblems(ref, label string, raw []byte) []string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return []string{label + ": not valid JSON: " + err.Error()}
	}
	var out []string
	for _, e := range packSchema().validate(ref, v) {
		out = append(out, label+" "+e)
	}
	return out
}

func readLimited(fsys fs.FS, name string) ([]byte, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxImportBytes+1))
	if err == nil && len(b) > maxImportBytes {
		err = fmt.Errorf("%s is too large", name)
	}
	return b, err
}

// packRoot finds pack.json at the top of fsys or inside a single top-level folder (how zips are usually made).
func packRoot(fsys fs.FS) (fs.FS, error) {
	if _, err := fs.Stat(fsys, "pack.json"); err == nil {
		return fsys, nil
	}
	entries, _ := fs.ReadDir(fsys, ".")
	var found []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "__") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := fs.Stat(fsys, path.Join(e.Name(), "pack.json")); err == nil {
			found = append(found, e.Name())
		}
	}
	if len(found) != 1 {
		return nil, errors.New("expected pack.json at the top level (or inside one folder)")
	}
	return fs.Sub(fsys, found[0])
}

// readSourceFS reads the folder layout: pack.json + modules/*.json, validating each file against the schema.
func readSourceFS(fsys fs.FS) (packFile, []moduleSource, error) {
	var base packFile
	root, err := packRoot(fsys)
	if err != nil {
		return base, nil, err
	}
	b, err := readLimited(root, "pack.json")
	if err != nil {
		return base, nil, err
	}
	if probs := schemaProblems("#/$defs/packSource", "pack.json", b); len(probs) > 0 {
		return base, nil, errors.New(strings.Join(probs, "\n  - "))
	}
	if err := json.Unmarshal(b, &base); err != nil {
		return base, nil, fmt.Errorf("pack.json: %w", err)
	}
	files, _ := fs.Glob(root, "modules/*.json")
	if len(files) == 0 {
		return base, nil, errors.New("no modules/*.json files found")
	}
	sort.Strings(files)
	var mods []moduleSource
	var probs []string
	for _, f := range files {
		mb, err := readLimited(root, f)
		if err != nil {
			return base, nil, err
		}
		if p := schemaProblems("#/$defs/module", f, mb); len(p) > 0 {
			probs = append(probs, p...)
			continue
		}
		var m moduleSource
		if err := json.Unmarshal(mb, &m); err != nil {
			return base, nil, fmt.Errorf("%s: %w", f, err)
		}
		mods = append(mods, m)
	}
	if len(probs) > 0 {
		return base, nil, errors.New(strings.Join(probs, "\n  - "))
	}
	return base, mods, nil
}

type bundleFile struct {
	Config        map[string]any               `json:"config"`
	Rubrics       map[string]map[string]string `json:"rubrics,omitempty"`
	MarkingSchema map[string]any               `json:"markingSchema,omitempty"`
	Meta          map[string]any               `json:"meta,omitempty"`
	Modules       []moduleSource               `json:"modules"`
}

func readSourceBundle(b []byte) (packFile, []moduleSource, error) {
	if probs := schemaProblems("#", "bundle", b); len(probs) > 0 {
		return packFile{}, nil, errors.New(strings.Join(probs, "\n  - "))
	}
	var bf bundleFile
	if err := json.Unmarshal(b, &bf); err != nil {
		return packFile{}, nil, err
	}
	return packFile{Config: bf.Config, Rubrics: bf.Rubrics, MarkingSchema: bf.MarkingSchema, Meta: bf.Meta}, bf.Modules, nil
}

func readSourceZip(b []byte) (packFile, []moduleSource, packAudio, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return packFile{}, nil, nil, err
	}
	var total uint64
	for _, f := range zr.File {
		total += f.UncompressedSize64
	}
	if len(zr.File) > maxZipEntries || total > 4*maxImportBytes {
		return packFile{}, nil, nil, errors.New("zip is too large")
	}
	base, mods, err := readSourceFS(zr)
	if err != nil {
		return base, mods, nil, err
	}
	a, err := readAudio(zr)
	return base, mods, a, err
}

// loadPackBytes accepts a zip, a bundle JSON or an already built data pack, and returns the finished pack.
func loadPackBytes(b []byte) (id string, p packFile, audio packAudio, err error) {
	var base packFile
	var mods []moduleSource
	switch {
	case bytes.HasPrefix(b, []byte("PK\x03\x04")):
		base, mods, audio, err = readSourceZip(b)
	case bytes.Contains(b[:min(len(b), 4096)], []byte("REVISION_SUBJECTS")):
		p, err = parsePack(b)
		if err == nil {
			if probs := validatePack(p); len(probs) > 0 {
				err = errors.New(strings.Join(probs, "\n  - "))
			}
		}
		return packID(p), p, nil, err
	default:
		base, mods, err = readSourceBundle(b)
	}
	if err != nil {
		return "", p, nil, err
	}
	id = strings.ReplaceAll(cfgString(base.Config, "storageId"), "_", "-")
	p, err = assemblePack(id, base, mods)
	if err == nil {
		err = checkAudioRefs(p, audio)
	}
	return id, p, audio, err
}

func packID(p packFile) string {
	return strings.ReplaceAll(cfgString(p.Config, "storageId"), "_", "-")
}

var privateInstallLocks sync.Map

func lockPrivateInstall(id string) (func(), error) {
	value, _ := privateInstallLocks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	privateDir := privatePacksDir()
	lockDir := filepath.Join(privateDir, ".locks")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		mu.Unlock()
		return nil, err
	}
	lockPath := filepath.Join(lockDir, id+".lock")
	deadline := time.Now().Add(30 * time.Second)
	for {
		f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
			return func() {
				_ = f.Close()
				_ = os.Remove(lockPath)
				mu.Unlock()
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			mu.Unlock()
			return nil, err
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > time.Minute {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			mu.Unlock()
			return nil, fmt.Errorf("another process is still installing pack %q", id)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func installPrivatePack(id string, p packFile, audio packAudio) error {
	if !validPackFile.MatchString("data/" + id + ".js") {
		return fmt.Errorf("pack id %q: use letters, digits, - and _ only", id)
	}
	if err := checkAudioRefs(p, audio); err != nil {
		return err
	}
	privateDir := privatePacksDir()
	if err := os.MkdirAll(privateDir, 0o755); err != nil {
		return err
	}
	unlock, err := lockPrivateInstall(id)
	if err != nil {
		return err
	}
	defer unlock()
	stage, err := os.MkdirTemp(privateDir, ".import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	stagedPack := filepath.Join(stage, id+".js")
	if err := writeFileAtomic(stagedPack, renderPackJS(p)); err != nil {
		return err
	}
	stagedAudio := ""
	if len(audio) > 0 {
		if stagedAudio, err = stageAudio(stage, id, audio); err != nil {
			return err
		}
	}

	finalPack := filepath.Join(privateDir, id+".js")
	finalAudio := audioDir(id)
	backupPack, backupAudio := filepath.Join(stage, "previous.js"), filepath.Join(stage, "previous-audio")
	installedPack, installedAudio := false, false
	rollback := func() {
		if installedPack {
			_ = os.Remove(finalPack)
		}
		if installedAudio {
			_ = os.RemoveAll(finalAudio)
		}
		if _, err := os.Stat(backupPack); err == nil {
			_ = os.Rename(backupPack, finalPack)
		}
		if _, err := os.Stat(backupAudio); err == nil {
			_ = os.MkdirAll(filepath.Dir(finalAudio), 0o755)
			_ = os.Rename(backupAudio, finalAudio)
		}
	}
	if _, err = os.Stat(finalPack); err == nil {
		if err = os.Rename(finalPack, backupPack); err != nil {
			return err
		}
	}
	if err = os.Rename(stagedPack, finalPack); err != nil {
		rollback()
		return err
	}
	installedPack = true
	if _, err = os.Stat(finalAudio); err == nil {
		if err = os.Rename(finalAudio, backupAudio); err != nil {
			rollback()
			return err
		}
	}
	if stagedAudio != "" {
		if err = os.MkdirAll(filepath.Dir(finalAudio), 0o755); err != nil {
			rollback()
			return err
		}
		if err = os.Rename(stagedAudio, finalAudio); err != nil {
			rollback()
			return err
		}
		installedAudio = true
	}
	return nil
}

type importResult struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Questions int    `json:"questions"`
	Modules   int    `json:"modules"`
}

func importBytes(b []byte) (importResult, error) {
	id, p, audio, err := loadPackBytes(b)
	if err != nil {
		return importResult{}, err
	}
	if err := installPrivatePack(id, p, audio); err != nil {
		return importResult{}, err
	}
	n := 0
	for _, qs := range p.Questions {
		n += len(qs)
	}
	return importResult{ID: id, Title: cfgString(p.Config, "brandTitle"), Questions: n, Modules: len(p.Notes)}, nil
}

// importPaths implements `gcse-revision import <bundle.json | folder | .zip>…`.
func importPaths(paths []string) error {
	if len(paths) == 0 {
		return errors.New("usage: gcse-revision import <bundle.json | folder | pack.zip> […]")
	}
	for _, p := range paths {
		var b []byte
		st, err := os.Stat(p)
		if err != nil {
			return err
		}
		if st.IsDir() {
			base, mods, err := readSourceFS(os.DirFS(p))
			if err != nil {
				return fmt.Errorf("%s:\n  - %w", p, err)
			}
			id := strings.ReplaceAll(cfgString(base.Config, "storageId"), "_", "-")
			out, err := assemblePack(id, base, mods)
			if err != nil {
				return err
			}
			audio, err := readAudio(os.DirFS(p))
			if err == nil {
				err = checkAudioRefs(out, audio)
			}
			if err != nil {
				return fmt.Errorf("%s:\n  - %w", p, err)
			}
			if err := installPrivatePack(id, out, audio); err != nil {
				return err
			}
			fmt.Printf("Imported %s: %d questions across %d modules → %s\n", cfgString(out.Config, "brandTitle"), out.Meta["questionTotal"], len(mods), filepath.Join(privatePacksDir(), id+".js"))
			continue
		}
		if b, err = os.ReadFile(p); err != nil {
			return err
		}
		res, err := importBytes(b)
		if err != nil {
			return fmt.Errorf("%s:\n  - %w", p, err)
		}
		fmt.Printf("Imported %s: %d questions across %d modules → %s\n", res.Title, res.Questions, res.Modules, filepath.Join(privatePacksDir(), res.ID+".js"))
	}
	fmt.Println("Restart the app (or reload the page) and pick it under Switch subject. It stays on this computer.")
	return nil
}

// exportPack converts a built data pack back into the open bundle format.
type exportModule struct {
	Paper       string           `json:"paper"`
	Module      string           `json:"module"`
	ModuleTitle string           `json:"moduleTitle"`
	Note        *packNote        `json:"note,omitempty"`
	Questions   []map[string]any `json:"questions"`
}

func exportPack(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	p, err := parsePack(b)
	if err != nil {
		return err
	}
	byModule := map[string]*exportModule{}
	var order []string
	for _, n := range p.Notes {
		k := n.Paper + "|" + n.Module
		note := packNote{SpecFocus: n.SpecFocus, SourcePages: n.SourcePages, Summary: n.Summary}
		byModule[k] = &exportModule{Paper: n.Paper, Module: n.Module, ModuleTitle: n.Title, Note: &note}
		order = append(order, k)
	}
	keys := make([]string, 0, len(p.Questions))
	for k := range p.Questions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		for _, q := range p.Questions[key] {
			k := q.Paper + "|" + q.Module
			m := byModule[k]
			if m == nil {
				m = &exportModule{Paper: q.Paper, Module: q.Module, ModuleTitle: q.ModuleTitle}
				byModule[k] = m
				order = append(order, k)
			}
			if m.ModuleTitle == "" {
				m.ModuleTitle = q.ModuleTitle
			}
			q.ID, q.Paper, q.Module, q.ModuleTitle, q.SourcePage = "", "", "", "", nil
			m.Questions = append(m.Questions, tidyQuestion(q))
		}
	}
	var modules []*exportModule
	for _, k := range order {
		modules = append(modules, byModule[k])
	}
	exported := map[string]any{"$schema": "schema/gcse-revision-pack.schema.json",
		"config": p.Config, "rubrics": p.Rubrics, "meta": p.Meta, "modules": modules}
	if p.MarkingSchema != nil {
		exported["markingSchema"] = p.MarkingSchema
	}
	out, err := json.MarshalIndent(exported, "", " ")
	if err != nil {
		return err
	}
	if probs := schemaProblems("#", "export", out); len(probs) > 0 {
		return errors.New(strings.Join(probs, "\n  - "))
	}
	if hasAudioRefs(p) {
		if !strings.EqualFold(filepath.Ext(dst), ".zip") {
			return errors.New("this pack contains audio; export it to a .zip so recordings are included")
		}
		return exportAudioZip(p, modules, dst)
	}
	return writeFileAtomic(dst, append(out, '\n'))
}

func exportAudioZip(p packFile, modules []*exportModule, dst string) error {
	id := packID(p)
	needed := map[string]bool{}
	for _, qs := range p.Questions {
		for _, q := range qs {
			if q.Audio != "" {
				needed[q.Audio] = true
			}
		}
	}
	basePack := map[string]any{
		"$schema": "../../schema/gcse-revision-pack.schema.json",
		"config":  p.Config,
		"rubrics": p.Rubrics,
		"meta":    p.Meta,
	}
	if p.MarkingSchema != nil {
		basePack["markingSchema"] = p.MarkingSchema
	}
	base, err := json.MarshalIndent(basePack, "", " ")
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, b []byte) error {
		w, err := zw.Create(name)
		if err == nil {
			_, err = w.Write(b)
		}
		return err
	}
	if err = add("pack.json", append(base, '\n')); err == nil {
		for i, m := range modules {
			mb, marshalErr := json.MarshalIndent(m, "", " ")
			if marshalErr != nil {
				err = marshalErr
				break
			}
			err = add(fmt.Sprintf("modules/%03d.json", i+1), append(mb, '\n'))
			if err != nil {
				break
			}
		}
	}
	if err == nil {
		for name := range needed {
			var b []byte
			b, err = os.ReadFile(filepath.Join(audioDir(id), name))
			if err != nil {
				err = fmt.Errorf("read audio %q: %w", name, err)
				break
			}
			if err = add("audio/"+name, b); err != nil {
				break
			}
		}
	}
	closeErr := zw.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return writeFileAtomic(dst, buf.Bytes())
}

// tidyQuestion drops fields the builder fills in itself (or that are empty) so exported files stay readable.
func tidyQuestion(q packQuestion) map[string]any {
	b, _ := json.Marshal(q)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"id", "paper", "module", "moduleTitle", "sourcePage", "rubric", "examTip", "answerTopic"} {
		if v, ok := m[k]; !ok || v == nil || v == "" {
			delete(m, k)
		}
	}
	return m
}
