package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// fixtureDecl is one upstream parser test file whose test cases join
// internal/release's corpus; TestFixtureAssertions reads their asserted
// labels.
type fixtureDecl struct {
	ID       string `json:"id"`
	Upstream string `json:"upstream"`
	Path     string `json:"path"`
}

// fixtureFile is one fixture as testdata/golden/fixtures.json holds it.
type fixtureFile struct {
	Upstream string          `json:"upstream"`
	Path     string          `json:"path"`
	Methods  []fixtureMethod `json:"methods"`
}

// fixtureMethod is one test method with [TestCase] rows. Body is the
// identifiers its body names, from which the reader tells what it asserts;
// each case is its arguments as JSON: a string, null, or {"code": text}.
type fixtureMethod struct {
	Name  string              `json:"name"`
	Body  []string            `json:"body"`
	Cases [][]json.RawMessage `json:"cases"`
}

func (g *generator) fixture(ctx context.Context, f *fixtureDecl) {
	repo, pin := g.ref(f.Upstream)
	src, err := g.up.file(ctx, repo, pin, f.Path)
	if err != nil {
		g.refuse(&refusal{id: f.ID, reason: err.Error()})
		return
	}
	methods, err := extractFixture(src)
	if err == nil && len(methods) == 0 {
		err = errors.New("no [TestCase] method: renamed or removed")
	}
	if err != nil {
		g.refuse(&refusal{id: f.ID, reason: fmt.Sprintf("%s: %v", f.Path, err)})
		return
	}
	g.fixtures[f.ID] = fixtureFile{Upstream: f.Upstream, Path: f.Path, Methods: methods}
}

// extractFixture reads every method a [TestCase] attribute run precedes.
func extractFixture(src []byte) ([]fixtureMethod, error) {
	toks, err := lexCSharp(string(src))
	if err != nil {
		return nil, err
	}
	var (
		out     []fixtureMethod
		pending [][]json.RawMessage
	)
	for i := 0; i < len(toks); i++ {
		switch {
		case toks[i].text == "[" && i+2 < len(toks) && toks[i+1].text == "TestCase" && toks[i+2].text == "(":
			args, end, err := attributeArgs(toks, i+3)
			if err != nil {
				return nil, err
			}
			pending = append(pending, args)
			i = end
		case toks[i].text == "void" && len(pending) > 0 && i+2 < len(toks) && toks[i+1].kind == tokIdent && toks[i+2].text == "(":
			body, end, err := methodBody(toks, i+2)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", toks[i+1].text, err)
			}
			out = append(out, fixtureMethod{Name: toks[i+1].text, Body: body, Cases: pending})
			pending, i = nil, end
		}
	}
	if len(pending) > 0 {
		return nil, errors.New("[TestCase] rows with no method after them")
	}
	return out, nil
}

// attributeArgs reads arguments from start up to the attribute's ")]",
// returning the index of its "]".
func attributeArgs(toks []csToken, start int) (args []json.RawMessage, end int, err error) {
	groups, end, err := splitArgs(toks, start)
	if err != nil {
		return nil, 0, err
	}
	if end+1 >= len(toks) || toks[end+1].text != "]" {
		return nil, 0, errors.New("[TestCase(...)] not closed")
	}
	for _, g := range groups {
		arg, err := literalArg(g)
		if err != nil {
			return nil, 0, err
		}
		args = append(args, arg)
	}
	return args, end + 1, nil
}

// splitArgs splits the tokens from start at depth-0 commas up to the ")"
// closing the argument list, returning that ")"'s index.
func splitArgs(toks []csToken, start int) (groups [][]csToken, end int, err error) {
	var group []csToken
	depth := 0
	for i := start; i < len(toks); i++ {
		switch t := toks[i]; {
		case depth == 0 && (t.text == "," || t.text == ")"):
			groups, group = append(groups, group), nil
			if t.text == ")" {
				return groups, i, nil
			}
			continue
		case t.text == "(":
			depth++
		case t.text == ")":
			depth--
		}
		group = append(group, toks[i])
	}
	return nil, 0, errors.New("unterminated [TestCase]")
}

// literalArg encodes one argument: a string literal as a JSON string, null
// as null, anything else as {"code": text}.
func literalArg(group []csToken) (json.RawMessage, error) {
	switch {
	case len(group) == 0:
		return nil, errors.New("an empty [TestCase] argument")
	case len(group) == 1 && group[0].kind == tokString:
		return compactJSON(group[0].text)
	case len(group) == 1 && group[0].text == "null":
		return json.RawMessage("null"), nil
	}
	var code strings.Builder
	for _, t := range group {
		if t.kind == tokString {
			return nil, fmt.Errorf("[TestCase] argument %q is not one literal", t.text)
		}
		code.WriteString(t.text)
	}
	return compactJSON(map[string]string{"code": code.String()})
}

// methodBody skips the parameter list at open and returns the identifiers of
// the braced body, with the index of its closing brace.
func methodBody(toks []csToken, open int) (body []string, end int, err error) {
	i := open
	for depth := 0; i < len(toks); i++ {
		switch toks[i].text {
		case "(":
			depth++
		case ")":
			depth--
		}
		if depth == 0 {
			break
		}
	}
	if i+1 >= len(toks) || toks[i+1].text != "{" {
		return nil, 0, errors.New("no method body")
	}
	depth := 0
	for j := i + 1; j < len(toks); j++ {
		switch t := toks[j]; {
		case t.text == "{":
			depth++
		case t.text == "}":
			depth--
			if depth == 0 {
				return body, j, nil
			}
		case t.kind == tokIdent:
			body = append(body, t.text)
		}
	}
	return nil, 0, errors.New("unterminated method body")
}

// fixturesJSON writes one case per line, so a pin bump's diff reads as the
// cases upstream added, changed or dropped.
func fixturesJSON(files map[string]fixtureFile) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("{")
	for i, id := range slices.Sorted(maps.Keys(files)) {
		if i > 0 {
			b.WriteString(",")
		}
		if err := writeFixture(&b, id, files[id]); err != nil {
			return nil, err
		}
	}
	b.WriteString("\n}\n")
	if !json.Valid(b.Bytes()) {
		return nil, errors.New("fixtures.json: invalid JSON")
	}
	return b.Bytes(), nil
}

func writeFixture(b *bytes.Buffer, id string, f fixtureFile) error {
	head, err := compactJSON(map[string]string{"upstream": f.Upstream, "path": f.Path})
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "\n  %q: {%s, \"methods\": [", id, head[1:len(head)-1])
	for j := range f.Methods {
		if j > 0 {
			b.WriteString(",")
		}
		if err := writeMethod(b, &f.Methods[j]); err != nil {
			return err
		}
	}
	b.WriteString("\n  ]}")
	return nil
}

func writeMethod(b *bytes.Buffer, m *fixtureMethod) error {
	name, err := compactJSON(m.Name)
	if err != nil {
		return err
	}
	body, err := compactJSON(m.Body)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "\n    {\"name\": %s, \"body\": %s, \"cases\": [", name, body)
	for k, c := range m.Cases {
		row, err := compactJSON(c)
		if err != nil {
			return err
		}
		if k > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n      ")
		b.Write(row)
	}
	b.WriteString("\n    ]}")
	return nil
}

// compactJSON is json.Marshal without HTML escaping, as indentJSON writes.
func compactJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	err := enc.Encode(v)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), err
}
