package main

// Subject-pack authoring tools:
//
//   gcse-revision build content/<pack-id>   assemble content/<pack-id>/{pack.json,modules/*.json} → data/<pack-id>.js
//   gcse-revision validate [data/x.js …]     check packs (default: every data/*.js)
//
// Source layout (diff-friendly, one file per spec module so several people/agents can work in parallel):
//
//   content/<pack-id>/pack.json        {"config":{…},"rubrics":{…},"meta":{…}}   (see CONTRIBUTING.md)
//   content/<pack-id>/modules/*.json   {"paper":"Paper 1","module":"1.1","moduleTitle":"…",
//                                       "note":{"specFocus":"…","summary":"…"},
//                                       "questions":[{"question":"…","marks":4,"command":"Outline",
//                                                     "modelAnswer":"…","evidence":[…],"examTip":"…","answerTopic":"…"}]}
//
// The builder fills in ids, paper/module fields, notes, moduleCounts and the meta totals.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const packPush = "window.REVISION_SUBJECTS.push("

type packPaper struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
}

type packQuestion struct {
	ID          string   `json:"id"`
	Paper       string   `json:"paper"`
	Module      string   `json:"module"`
	ModuleTitle string   `json:"moduleTitle"`
	Question    string   `json:"question"`
	Marks       int      `json:"marks"`
	Command     string   `json:"command"`
	SourcePage  any      `json:"sourcePage"`
	Rubric      string   `json:"rubric"`
	ModelAnswer string   `json:"modelAnswer"`
	Evidence    []string `json:"evidence"`
	ExamTip     string   `json:"examTip"`
	AnswerTopic string   `json:"answerTopic"`
	Audio       string   `json:"audio,omitempty"`
}

type packNote struct {
	ID          string `json:"id"`
	Paper       string `json:"paper"`
	Module      string `json:"module"`
	Title       string `json:"title"`
	SpecFocus   string `json:"specFocus"`
	SourcePages string `json:"sourcePages"`
	Summary     string `json:"summary"`
}

type packFile struct {
	Config        map[string]any               `json:"config"`
	Questions     map[string][]packQuestion    `json:"questions"`
	Notes         []packNote                   `json:"notes"`
	Rubrics       map[string]map[string]string `json:"rubrics,omitempty"`
	MarkingSchema map[string]any               `json:"markingSchema,omitempty"`
	ModuleCounts  map[string]int               `json:"moduleCounts,omitempty"`
	Meta          map[string]any               `json:"meta,omitempty"`
}

func parsePack(b []byte) (packFile, error) {
	var p packFile
	s := string(b)
	i := strings.Index(s, packPush)
	if i < 0 {
		return p, errors.New("expected " + packPush + "{…});")
	}
	body := strings.TrimSpace(s[i+len(packPush):])
	body = strings.TrimSuffix(strings.TrimSuffix(body, ";"), ")")
	dec := json.NewDecoder(strings.NewReader(body))
	if err := dec.Decode(&p); err != nil {
		return p, fmt.Errorf("pack body must be a single JSON object: %w", err)
	}
	return p, nil
}

func cfgString(c map[string]any, k string) string {
	if v, ok := c[k].(string); ok {
		return v
	}
	return ""
}

func packPapers(p packFile) []packPaper {
	var papers []packPaper
	b, _ := json.Marshal(p.Config["papers"])
	_ = json.Unmarshal(b, &papers)
	return papers
}

// validatePack returns a list of problems (empty = OK).
func validatePack(p packFile) []string {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }
	for _, k := range []string{"storageId", "examBoard", "subjectArea", "subjectName", "brandTitle"} {
		if cfgString(p.Config, k) == "" {
			add("config.%s is required", k)
		}
	}
	if id := cfgString(p.Config, "storageId"); id != "" && !regexp.MustCompile(`^[a-z0-9_]+$`).MatchString(id) {
		add("config.storageId %q: use lowercase letters, digits and _ only", id)
	}
	papers := packPapers(p)
	if len(papers) == 0 {
		add("config.papers must list at least one paper")
	}
	paperIDs := map[string]bool{}
	for _, pp := range papers {
		paperIDs[pp.ID] = true
	}
	ids := map[string]bool{}
	modules := map[string]bool{}
	total := 0
	for key, qs := range p.Questions {
		for i, q := range qs {
			where := fmt.Sprintf("questions.%s[%d] (%s)", key, i, q.ID)
			total++
			if q.ID == "" || ids[q.ID] {
				add("%s: id missing or duplicated", where)
			}
			ids[q.ID] = true
			if !paperIDs[q.Paper] {
				add("%s: paper %q isn't in config.papers", where, q.Paper)
			}
			if strings.TrimSpace(q.Question) == "" || strings.TrimSpace(q.ModelAnswer) == "" {
				add("%s: question and modelAnswer are required", where)
			}
			if q.Marks <= 0 || q.Command == "" || q.Module == "" {
				add("%s: marks, command and module are required", where)
			}
			modules[q.Paper+"|"+q.Module] = true
		}
	}
	if total == 0 {
		add("pack has no questions")
	}
	for i, n := range p.Notes {
		if n.Paper == "" || n.Module == "" || strings.TrimSpace(n.Summary) == "" {
			add("notes[%d] (%s): paper, module and summary are required", i, n.ID)
		}
	}
	_ = modules
	sort.Strings(errs)
	return errs
}

// packWarnings: non-fatal gaps worth fixing (e.g. a module with questions but no revision note anywhere).
func packWarnings(p packFile) []string {
	noted := map[string]bool{}
	for _, n := range p.Notes {
		noted[n.Paper+"|"+n.Module], noted["*|"+n.Module] = true, true
	}
	seen := map[string]bool{}
	var w []string
	for _, qs := range p.Questions {
		for _, q := range qs {
			k := q.Paper + "|" + q.Module
			if !seen[k] && !noted[k] && !noted["*|"+q.Module] {
				w = append(w, fmt.Sprintf("%s %s has questions but no revision note", q.Paper, q.Module))
			}
			seen[k] = true
		}
	}
	sort.Strings(w)
	return w
}

func validateFiles(paths []string) error {
	full := len(paths) == 0
	if full {
		paths, _ = filepath.Glob(filepath.Join("data", "*.js"))
		dirs, _ := filepath.Glob(filepath.Join("content", "*"))
		paths = append(paths, dirs...)
	}
	bad := 0
	if full { // full check: every shipped pack must be generated from an open-format source folder
		built, _ := filepath.Glob(filepath.Join("data", "*.js"))
		for _, f := range built {
			id := strings.TrimSuffix(filepath.Base(f), ".js")
			src := filepath.Join("content", id)
			base, mods, err := readSourceFS(os.DirFS(src))
			if err == nil {
				var out packFile
				if out, err = assemblePack(id, base, mods); err == nil {
					have, _ := os.ReadFile(f)
					if !bytes.Equal(have, renderPackJS(out)) {
						err = fmt.Errorf("out of date: run `go run . build %s`", src)
					}
				}
			}
			if err != nil {
				bad++
				fmt.Printf("FAIL %s is not generated from %s\n     - %s\n", f, src, err)
			}
		}
	}
	for _, f := range paths {
		if st, err := os.Stat(f); err == nil && st.IsDir() {
			base, mods, err := readSourceFS(os.DirFS(f))
			if err == nil {
				_, err = assemblePack(filepath.Base(f), base, mods)
			}
			if err != nil {
				bad++
				fmt.Printf("FAIL %s\n     - %s\n", f, err)
			} else {
				fmt.Printf("ok   %s (schema + pack checks, %d modules)\n", f, len(mods))
			}
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		p, err := parsePack(b)
		problems := []string{}
		if err != nil {
			problems = append(problems, err.Error())
		} else {
			problems = validatePack(p)
		}
		if len(problems) == 0 {
			n := 0
			for _, qs := range p.Questions {
				n += len(qs)
			}
			fmt.Printf("ok   %s (%d questions, %d notes)\n", f, n, len(p.Notes))
			for _, w := range packWarnings(p) {
				fmt.Println("     warning:", w)
			}
			continue
		}
		bad++
		fmt.Printf("FAIL %s\n", f)
		for _, e := range problems {
			fmt.Println("     -", e)
		}
	}
	if bad > 0 {
		return fmt.Errorf("%d pack(s) failed validation", bad)
	}
	return nil
}

type moduleSource struct {
	Paper       string         `json:"paper"`
	Module      string         `json:"module"`
	ModuleTitle string         `json:"moduleTitle"`
	Note        packNote       `json:"note"`
	Questions   []packQuestion `json:"questions"`
}

// naturalLess orders "1.2" < "1.10" and "Theme A" < "Theme B".
func naturalLess(a, b string) bool {
	pa, pb := strings.FieldsFunc(a, func(r rune) bool { return r == '.' || r == ' ' }), strings.FieldsFunc(b, func(r rune) bool { return r == '.' || r == ' ' })
	for i := 0; i < len(pa) && i < len(pb); i++ {
		na, ea := strconv.Atoi(pa[i])
		nb, eb := strconv.Atoi(pb[i])
		if ea == nil && eb == nil && na != nb {
			return na < nb
		}
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

func buildPack(srcDir, outDir string) error {
	id := filepath.Base(filepath.Clean(srcDir))
	if !validPackFile.MatchString("data/" + id + ".js") {
		return fmt.Errorf("content folder name %q: use letters, digits, - and _ only", id)
	}
	base, mods, err := readSourceFS(os.DirFS(srcDir))
	if err != nil {
		return err
	}
	out, err := assemblePack(id, base, mods)
	if err != nil {
		return err
	}
	if hasAudioRefs(out) {
		return errors.New("audio recordings are only supported for imported (private) packs, not published ones; remove the audio fields")
	}
	dst := filepath.Join(outDir, "data", id+".js")
	if err := writeFileAtomic(dst, renderPackJS(out)); err != nil {
		return err
	}
	fmt.Printf("Built %s: %d questions across %d modules.\n", dst, out.Meta["questionTotal"], len(mods))
	return nil
}

func renderPackJS(p packFile) []byte {
	js, _ := json.Marshal(p)
	return []byte("window.REVISION_SUBJECTS=window.REVISION_SUBJECTS||[];\n" + packPush + string(js) + ");\n")
}

// assemblePack turns pack-level settings plus module sources into the pack the app loads: it fills in ids,
// paper/module fields, notes, moduleCounts and the meta totals, then validates the result.
func assemblePack(id string, base packFile, mods []moduleSource) (packFile, error) {
	papers := packPapers(base)
	paperKey := map[string]string{}
	for i, pp := range papers {
		paperKey[pp.ID] = "paper" + strconv.Itoa(i+1)
	}
	for _, m := range mods {
		if paperKey[m.Paper] == "" {
			return packFile{}, fmt.Errorf("module %s: paper %q isn't one of config.papers", m.Module, m.Paper)
		}
	}
	sort.SliceStable(mods, func(i, j int) bool {
		if mods[i].Paper != mods[j].Paper {
			return paperKey[mods[i].Paper] < paperKey[mods[j].Paper]
		}
		return naturalLess(mods[i].Module, mods[j].Module)
	})

	prefix := strings.ReplaceAll(id, "_", "-")
	out := base
	if out.MarkingSchema == nil && cfgString(base.Config, "subjectArea") == "Religious Studies" {
		out.MarkingSchema = defaultReligiousStudiesMarkingSchema(cfgString(base.Config, "examBoard"))
		if out.MarkingSchema != nil {
			out.Config = cloneAnyMap(base.Config)
			out.Config["markingSchemaId"] = out.MarkingSchema["id"]
		}
	}
	out.Questions = map[string][]packQuestion{}
	out.ModuleCounts = map[string]int{}
	out.Notes = nil
	for _, m := range mods {
		key := paperKey[m.Paper]
		out.ModuleCounts[key]++
		slug := strings.NewReplacer(" ", "", ".", "-").Replace(strings.ToLower(m.Module))
		n := m.Note
		n.ID, n.Paper, n.Module = prefix+"-"+key+"-"+slug, m.Paper, m.Module
		if n.Title == "" {
			n.Title = m.ModuleTitle
		}
		if n.SourcePages == "" {
			n.SourcePages = "Written to match the published " + cfgString(base.Config, "examBoard") + " specification."
		}
		if strings.TrimSpace(n.Summary) != "" {
			out.Notes = append(out.Notes, n)
		}
		for i, q := range m.Questions {
			q.ID = fmt.Sprintf("%s-%s-%s-%d", prefix, key, slug, i)
			if ip, _ := base.Meta["idPrefix"].(string); ip != "" { // keeps ids (and so saved progress) stable for packs that predate the folder layout
				q.ID = fmt.Sprintf("%s-p%s-%s-%d", ip, strings.TrimPrefix(key, "paper"), slug, i)
			}
			q.Paper, q.Module, q.ModuleTitle = m.Paper, m.Module, m.ModuleTitle
			if q.Evidence == nil {
				q.Evidence = []string{}
			}
			out.Questions[key] = append(out.Questions[key], q)
		}
	}
	out.Meta = map[string]any{}
	for k, v := range base.Meta {
		out.Meta[k] = v
	}
	total := 0
	for i := range papers {
		key := "paper" + strconv.Itoa(i+1)
		out.Meta[key+"QuestionCount"] = len(out.Questions[key])
		total += len(out.Questions[key])
	}
	out.Meta["questionTotal"], out.Meta["modulesTotal"] = total, len(mods)

	if problems := validatePack(out); len(problems) > 0 {
		return packFile{}, fmt.Errorf("%s:\n  - %s", id, strings.Join(problems, "\n  - "))
	}
	return out, nil
}

func cloneAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func defaultReligiousStudiesMarkingSchema(board string) map[string]any {
	lower := strings.ToLower(board)
	switch {
	case strings.Contains(lower, "aqa"):
		return map[string]any{
			"id": "aqa-rs-generic", "title": "AQA Religious Studies · check and adapt",
			"status": "adaptable", "version": 1,
			"sourceNotes": "AQA GCSE RS A uses 1/1/4/6/12 question structures and a separate SPaG overlay. Check the current official specification and mark scheme before relying on a mark estimate.",
			"dsl": `SCHEME AQA Religious Studies adaptable guidance
COMMAND ALL
MARKS AUTO
REQUIRE use accurate subject knowledge and answer the command word
REQUIRE support religious or non-religious views where the question asks for them
NEVER invent quotations, sources or official level descriptors
FEEDBACK be specific, supportive and honest about missing content

COMMAND Explain
MARKS AUTO
AWARD 1 one accurate relevant point
AWARD 1 a clear linked explanation

COMMAND Evaluate
MARKS AUTO
BAND 9-12 well-argued evaluation with a justified conclusion
BAND 5-8 relevant discussion with some evaluation
BAND 1-4 limited points with little evaluation`,
			"references": []map[string]any{
				{"title": "AQA Religious Studies 8062 specification", "url": "https://www.aqa.org.uk/subjects/religious-studies/gcse/religious-studies-8062/specification"},
				{"title": "AQA scheme of assessment", "url": "https://www.aqa.org.uk/subjects/religious-studies/gcse/religious-studies-8062/specification/scheme-of-assessment"},
			},
		}
	case strings.Contains(lower, "edexcel") || strings.Contains(lower, "pearson"):
		return map[string]any{
			"id": "edexcel-rs-generic", "title": "Edexcel Religious Studies · check and adapt",
			"status": "adaptable", "version": 1,
			"sourceNotes": "Built from verified public structure and kept generic. Check the official Pearson specification and current mark schemes before relying on a mark estimate.",
			"dsl": `SCHEME Edexcel Religious Studies adaptable guidance
COMMAND ALL
MARKS AUTO
REQUIRE answer the exact question and use relevant subject knowledge
REQUIRE explain ideas in language suited to the marks available
NEVER invent quotations, teachings or official mark allocations
FEEDBACK identify what is accurate, what is missing, and one precise next step

COMMAND Explain
MARKS AUTO
AWARD 1 one accurate and relevant point
AWARD 1 a clear explanation or supporting development

COMMAND Evaluate
MARKS AUTO
BAND 9-12 balanced evaluation with a supported judgement
BAND 5-8 relevant arguments with some evaluation
BAND 1-4 limited points or description`,
			"references": []map[string]any{
				{"title": "Edexcel International GCSE Religious Studies course materials", "url": "https://qualifications.pearson.com/en/qualifications/edexcel-international-gcses/international-gcse-religious-studies-2017.coursematerials.html"},
				{"title": "Pearson past papers and mark schemes", "url": "https://qualifications.pearson.com/en/support/support-topics/exams/past-papers.html"},
			},
		}
	default:
		return nil
	}
}

// privatePacksDir holds packs that are only ever used on this computer (never published or synced),
// e.g. material you're licensed to use yourself but not to redistribute. Drop data-pack .js files in it.
func privatePacksDir() string {
	if d := os.Getenv("GCSE_REVISION_PRIVATE_PACKS"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(dataDir(), "private-packs")
	}
	return filepath.Join(base, appName, "packs")
}

const privatePrefix = "private/"

// privatePackSources lists private packs: those baked into the binary, overridden by the on-disk folder.
func privatePackSources() map[string][]byte {
	out := map[string][]byte{}
	for n, b := range embeddedPrivatePacks() {
		out[n] = b
	}
	files, _ := filepath.Glob(filepath.Join(privatePacksDir(), "*.js"))
	for _, f := range files {
		if b, err := os.ReadFile(f); err == nil {
			out[filepath.Base(f)] = b
		}
	}
	return out
}

func privatePacks() []subjectEntry {
	var out []subjectEntry
	srcs := privatePackSources()
	names := make([]string, 0, len(srcs))
	for n := range srcs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		b := srcs[name]
		if !validPackFile.MatchString("data/" + name) {
			continue
		}
		id := firstMatch(reStorageID, b)
		if id == "" {
			continue
		}
		out = append(out, subjectEntry{ID: id, Title: firstMatch(reBrandTitle, b), Board: firstMatch(reExamBoard, b),
			Category: firstMatch(reSubjectArea, b), File: "data/" + privatePrefix + name, SHA256: sha256Hex(b), Bytes: len(b)})
	}
	return out
}

func readPrivatePack(name string) ([]byte, error) {
	name = strings.TrimPrefix(name, "data/"+privatePrefix)
	if b, ok := privatePackSources()[name]; ok && validPackFile.MatchString("data/"+name) {
		return b, nil
	}
	return nil, os.ErrNotExist
}
