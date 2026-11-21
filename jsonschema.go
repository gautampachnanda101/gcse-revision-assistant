package main

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// A small JSON Schema (2020-12) validator covering the keywords our pack schema uses: type, required,
// properties, additionalProperties (schema or bool), items, enum, const, pattern, minLength, maxLength,
// minimum, maximum, minItems and local "#/$defs/…" $refs. It keeps the app standard-library only.

type schemaDoc struct{ root map[string]any }

func parseSchema(b []byte) (*schemaDoc, error) {
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, err
	}
	return &schemaDoc{root: root}, nil
}

// validate checks v against the schema at ref ("#" or "#/$defs/name") and returns "path: problem" lines.
func (s *schemaDoc) validate(ref string, v any) []string {
	node, err := s.resolve(ref)
	if err != nil {
		return []string{err.Error()}
	}
	var errs []string
	s.check(node, v, "$", &errs)
	sort.Strings(errs)
	return errs
}

func (s *schemaDoc) resolve(ref string) (map[string]any, error) {
	if ref == "#" || ref == "" {
		return s.root, nil
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("unsupported $ref %q", ref)
	}
	var cur any = s.root
	for _, part := range strings.Split(ref[2:], "/") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("bad $ref %q", ref)
		}
		cur = m[part]
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("bad $ref %q", ref)
	}
	return m, nil
}

func jsonType(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		if x == math.Trunc(x) {
			return "integer"
		}
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

func typeMatches(want, got string) bool {
	return want == got || (want == "number" && got == "integer")
}

func (s *schemaDoc) check(node map[string]any, v any, at string, errs *[]string) {
	fail := func(f string, a ...any) { *errs = append(*errs, at+": "+fmt.Sprintf(f, a...)) }
	if ref, ok := node["$ref"].(string); ok {
		target, err := s.resolve(ref)
		if err != nil {
			fail("%v", err)
			return
		}
		s.check(target, v, at, errs)
	}
	if t, ok := node["type"]; ok {
		var want []string
		switch x := t.(type) {
		case string:
			want = []string{x}
		case []any:
			for _, e := range x {
				want = append(want, fmt.Sprint(e))
			}
		}
		got, matched := jsonType(v), false
		for _, w := range want {
			matched = matched || typeMatches(w, got)
		}
		if !matched {
			fail("expected %s, got %s", strings.Join(want, " or "), got)
			return
		}
	}
	if e, ok := node["enum"].([]any); ok {
		found := false
		for _, c := range e {
			found = found || fmt.Sprint(c) == fmt.Sprint(v)
		}
		if !found {
			fail("must be one of %v", e)
		}
	}
	if c, ok := node["const"]; ok && fmt.Sprint(c) != fmt.Sprint(v) {
		fail("must be %v", c)
	}
	switch x := v.(type) {
	case string:
		n := len([]rune(x))
		if m, ok := node["minLength"].(float64); ok && float64(n) < m {
			fail("must be at least %d character(s)", int(m))
		}
		if m, ok := node["maxLength"].(float64); ok && float64(n) > m {
			fail("must be at most %d characters", int(m))
		}
		if p, ok := node["pattern"].(string); ok {
			if re, err := regexp.Compile(p); err == nil && !re.MatchString(x) {
				fail("%q doesn't match %s", x, p)
			}
		}
	case float64:
		if m, ok := node["minimum"].(float64); ok && x < m {
			fail("must be at least %v", m)
		}
		if m, ok := node["maximum"].(float64); ok && x > m {
			fail("must be at most %v", m)
		}
	case []any:
		if m, ok := node["minItems"].(float64); ok && float64(len(x)) < m {
			fail("needs at least %d item(s)", int(m))
		}
		if items, ok := node["items"].(map[string]any); ok {
			for i, e := range x {
				s.check(items, e, fmt.Sprintf("%s[%d]", at, i), errs)
			}
		}
	case map[string]any:
		if req, ok := node["required"].([]any); ok {
			for _, r := range req {
				if _, has := x[fmt.Sprint(r)]; !has {
					fail("missing required property %q", r)
				}
			}
		}
		props, _ := node["properties"].(map[string]any)
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if sub, ok := props[k].(map[string]any); ok {
				s.check(sub, x[k], at+"."+k, errs)
				continue
			}
			switch ap := node["additionalProperties"].(type) {
			case bool:
				if !ap {
					fail("unexpected property %q", k)
				}
			case map[string]any:
				s.check(ap, x[k], at+"."+k, errs)
			}
		}
	}
}
