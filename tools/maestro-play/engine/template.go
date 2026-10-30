package engine

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"
)

// funcs are the only functions templates get, besides text/template's own.
var funcs = template.FuncMap{
	"json": func(v any) (string, error) {
		b, err := json.Marshal(v)
		return string(b), err
	},
	// join takes the list and the separator in either order.
	"join": func(a, b any) string {
		if sep, ok := a.(string); ok {
			if _, isStr := b.(string); !isStr {
				return joinList(b, sep)
			}
		}
		return joinList(a, str(b))
	},
	"int":   toIntAny,
	"index": safeIndex,
	"add": func(a any, more ...any) int {
		n := toIntAny(a)
		for _, m := range more {
			n += toIntAny(m)
		}
		return n
	},
	// quote shell-quotes one argument for a ticket: or run: line.
	"quote": func(v any) string { return shellQuote(str(v)) },
	// latest is the newest comment body starting with prefix, or "".
	"latest": func(t any, prefix string) string {
		cs := comments(t)
		for i := len(cs) - 1; i >= 0; i-- {
			if strings.HasPrefix(cs[i], prefix) {
				return cs[i]
			}
		}
		return ""
	},
	// line is the value of the last "<key>: " line of text, or "".
	"line": func(key string, text any) string { return lineValue(key, str(text)) },
	// latestLine is line over the ticket's comments, newest first.
	"latestLine": func(t any, key string) string { return latestLine(comments(t), key) },
	"hasPrefix":  func(s any, prefix string) bool { return strings.HasPrefix(str(s), prefix) },
	"contains":   func(s any, sub string) bool { return strings.Contains(str(s), sub) },
	"trim":       func(s any) string { return strings.TrimSpace(str(s)) },
	"lower":      func(s any) string { return strings.ToLower(str(s)) },
	"default": func(def, v any) any {
		if v == nil || v == "" {
			return def
		}
		return v
	},
}

// safeIndex is text/template's index, except that indexing nothing (a
// step that hasn't run), a missing key or an index out of range gives nil,
// so {{index .steps.x.json "k"}} is safe for an optional read.
func safeIndex(v any, keys ...any) (any, error) {
	cur := reflect.ValueOf(v)
	for _, k := range keys {
		for cur.IsValid() && (cur.Kind() == reflect.Interface || cur.Kind() == reflect.Pointer) {
			if cur.IsNil() {
				return nil, nil
			}
			cur = cur.Elem()
		}
		if !cur.IsValid() {
			return nil, nil
		}
		switch cur.Kind() {
		case reflect.Map:
			kv := reflect.ValueOf(k)
			if !kv.IsValid() || !kv.Type().AssignableTo(cur.Type().Key()) {
				return nil, fmt.Errorf("index: key %v doesn't fit %s", k, cur.Type())
			}
			cur = cur.MapIndex(kv)
		case reflect.Slice, reflect.Array, reflect.String:
			i := toInt(k)
			if i < 0 || i >= cur.Len() {
				return nil, nil
			}
			cur = cur.Index(i)
		default:
			return nil, fmt.Errorf("index: can't index a %s", cur.Kind())
		}
	}
	if !cur.IsValid() {
		return nil, nil
	}
	return cur.Interface(), nil
}

func joinList(v any, sep string) string {
	switch l := v.(type) {
	case []string:
		return strings.Join(l, sep)
	case []any:
		parts := make([]string, len(l))
		for i, x := range l {
			parts[i] = str(x)
		}
		return strings.Join(parts, sep)
	case nil:
		return ""
	}
	return str(v)
}

func toIntAny(v any) int {
	if s, ok := v.(string); ok {
		n, _ := strconv.Atoi(strings.TrimSpace(s))
		return n
	}
	return toInt(v)
}

// comments are a ticket's comment bodies, oldest first.
func comments(t any) []string {
	m, _ := t.(map[string]any)
	cs, _ := m["comments"].([]any)
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		if cm, ok := c.(map[string]any); ok {
			out = append(out, str(cm["body"]))
		}
	}
	return out
}

// lineValue is the value of the last "<key>:" line of text with one, or
// "". The last, because a note writes its trailers after any reply text,
// which may carry a forged line of its own.
func lineValue(key, text string) string {
	v := ""
	for _, l := range strings.Split(text, "\n") {
		if after, ok := strings.CutPrefix(strings.TrimSpace(l), key+":"); ok && strings.TrimSpace(after) != "" {
			v = strings.TrimSpace(after)
		}
	}
	return v
}

// latestLine is lineValue of the newest comment that has the key. It is
// the one trailer reader: latestLine, .ticket.trailers and the author's
// family all go through it, so they never disagree.
func latestLine(bodies []string, key string) string {
	for i := len(bodies) - 1; i >= 0; i-- {
		if v := lineValue(key, bodies[i]); v != "" {
			return v
		}
	}
	return ""
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`*?[]{}()<>|&;#~!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// parseTemplate parses text with the engine's functions. Missing map keys
// are errors, so a misspelled name never renders as "<no value>".
func parseTemplate(name, text string) (*template.Template, error) {
	return template.New(name).Funcs(funcs).Option("missingkey=error").Parse(text)
}

func renderTemplate(name, text string, data any) (string, error) {
	t, err := parseTemplate(name, text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// renderBool renders a when, gate or until template, which must print true
// or false.
func renderBool(name, text string, data any) (bool, error) {
	out, err := renderTemplate(name, text, data)
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(out) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("%s rendered %q, want true or false", name, strings.TrimSpace(out))
}

// word is one argument of a ticket: or run: line, before rendering.
type word struct {
	text   string
	quoted bool
}

// splitWords splits a line into arguments the way a shell would split
// quotes, with no expansion. With actions set, {{ }} is one unit, for
// reading a template's words before it's rendered.
func splitWords(s string) ([]word, error) { return split(s, true) }

func split(s string, actions bool) ([]word, error) {
	var out []word
	var cur strings.Builder
	inWord, quoted := false, false
	var quote byte
	flush := func() {
		if inWord {
			out = append(out, word{cur.String(), quoted})
		}
		cur.Reset()
		inWord, quoted = false, false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if actions && strings.HasPrefix(s[i:], "{{") {
			end := strings.Index(s[i:], "}}")
			if end < 0 {
				return nil, fmt.Errorf("unclosed {{ in %q", s)
			}
			cur.WriteString(s[i : i+end+2])
			inWord = true
			i += end + 1
			continue
		}
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && quote == '"' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
				i++
				cur.WriteByte(s[i])
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote, inWord, quoted = c, true, true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			inWord = true
		case c == ' ' || c == '\t' || c == '\n':
			flush()
		default:
			cur.WriteByte(c)
			inWord = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed %c quote in %q", quote, s)
	}
	flush()
	return out, nil
}

// renderArgs renders a step line, then splits it like a shell would, with
// no expansion. A value with spaces needs quote (or quotes in the line);
// a list can be spread with join.
func renderArgs(name, line string, data any) ([]string, error) {
	out, err := renderTemplate(name, line, data)
	if err != nil {
		return nil, err
	}
	words, err := split(out, false)
	if err != nil {
		return nil, fmt.Errorf("%s: %w (use quote on values with quotes or spaces)", name, err)
	}
	args := make([]string, len(words))
	for i, w := range words {
		args[i] = w.text
	}
	return args, nil
}

// checkFields walks a parsed template and reports every field chain on the
// root data, or on $, that names a key the schema doesn't have. The schema
// is the lint data: nested maps whose keys are every name a template may
// use; a non-map value ends the check for that chain. Fields inside with
// and range bodies are relative to a different dot, so only $ chains are
// checked there.
func checkFields(t *template.Template, schema map[string]any) []string {
	var probs []string
	seen := map[string]bool{}
	report := func(path []string) {
		if p := missingPath(schema, path); p != "" && !seen[p] {
			seen[p] = true
			probs = append(probs, "unknown field "+p)
		}
	}
	var walk func(n parse.Node, rootDot bool)
	walkPipe := func(p *parse.PipeNode, rootDot bool) {
		if p == nil {
			return
		}
		for _, c := range p.Cmds {
			for _, a := range c.Args {
				walk(a, rootDot)
			}
		}
	}
	walk = func(n parse.Node, rootDot bool) {
		switch n := n.(type) {
		case nil:
		case *parse.ListNode:
			if n == nil {
				return
			}
			for _, c := range n.Nodes {
				walk(c, rootDot)
			}
		case *parse.ActionNode:
			walkPipe(n.Pipe, rootDot)
		case *parse.PipeNode:
			walkPipe(n, rootDot)
		case *parse.IfNode:
			walkPipe(n.Pipe, rootDot)
			walk(n.List, rootDot)
			walk(n.ElseList, rootDot)
		case *parse.WithNode:
			walkPipe(n.Pipe, rootDot)
			walk(n.List, false)
			walk(n.ElseList, rootDot)
		case *parse.RangeNode:
			walkPipe(n.Pipe, rootDot)
			walk(n.List, false)
			walk(n.ElseList, rootDot)
		case *parse.TemplateNode:
			walkPipe(n.Pipe, rootDot)
		case *parse.FieldNode:
			if rootDot {
				report(n.Ident)
			}
		case *parse.VariableNode:
			if len(n.Ident) > 1 && n.Ident[0] == "$" {
				report(n.Ident[1:])
			}
		case *parse.ChainNode:
			walk(n.Node, rootDot)
		}
	}
	for _, tt := range t.Templates() {
		if tt.Tree != nil {
			walk(tt.Tree.Root, true)
		}
	}
	return probs
}

// missingPath returns ".a.b" for the first key of path the schema lacks, or "".
func missingPath(schema map[string]any, path []string) string {
	var cur any = schema
	for i, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		v, ok := m[k]
		if !ok {
			return "." + strings.Join(path[:i+1], ".")
		}
		cur = v
	}
	return ""
}
