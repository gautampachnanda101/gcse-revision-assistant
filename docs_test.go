package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The docs must render the same in every Markdown viewer: GitHub, Backstage TechDocs, MkDocs and browser
// extensions that don't support GitHub-flavoured Markdown. So they may use only core Markdown: indented code
// blocks instead of ``` fences, lists instead of tables, and SVG images (docs/diagrams) instead of Mermaid
// blocks. Code lines must also fit the TechDocs content column.
func TestDocsUsePortableMarkdown(t *testing.T) {
	tableRow := regexp.MustCompile(`^\|.*\|\s*$`)
	var pages []string
	err := filepath.Walk("docs", func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, ".md") {
			pages = append(pages, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) < 10 {
		t.Fatalf("found only %d doc pages", len(pages))
	}
	for _, p := range pages {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			at := p + ":" + itoa(i+1)
			switch {
			case strings.HasPrefix(strings.TrimSpace(line), "```"):
				t.Errorf("%s: fenced code block; indent the code by 4 spaces instead", at)
			case tableRow.MatchString(line):
				t.Errorf("%s: table; use a list instead", at)
			case strings.HasPrefix(line, "    ") && len(strings.TrimSpace(line)) > 72 && !strings.HasPrefix(strings.TrimSpace(line), "- "):
				t.Errorf("%s: code line longer than 72 characters", at)
			}
		}
	}
}

func TestDocDiagramsExist(t *testing.T) {
	srcs, _ := filepath.Glob("docs/diagrams/src/*.mmd")
	if len(srcs) == 0 {
		t.Fatal("no diagram sources")
	}
	for _, src := range srcs {
		svg := filepath.Join("docs", "diagrams", strings.TrimSuffix(filepath.Base(src), ".mmd")+".svg")
		if _, err := os.Stat(svg); err != nil {
			t.Errorf("%s has no rendered %s; run sh scripts/render-diagrams.sh", src, svg)
		}
	}
}
