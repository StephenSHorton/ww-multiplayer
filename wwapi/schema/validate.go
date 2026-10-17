package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Schema is a compiled JSON Schema.
type Schema struct {
	root *node
	defs map[string]*node
}

type node struct {
	always *bool // a boolean schema: true accepts anything, false nothing

	ref string // the $defs name, resolved at validation time

	types    []string
	enum     []any
	hasConst bool
	constV   any
	not      *node

	props         map[string]*node
	required      []string
	addl          *node
	propertyNames *node
	minProps      int
	maxProps      int // -1: no limit

	items    *node
	minItems int
	maxItems int // -1: no limit
	unique   bool

	pattern *regexp.Regexp
	minLen  int
	maxLen  int // -1: no limit

	minimum, maximum *big.Rat
}

var annotations = map[string]bool{
	"$schema": true, "$id": true, "$comment": true, "title": true,
	"description": true, "default": true, "examples": true,
}

var knownTypes = map[string]bool{
	"null": true, "boolean": true, "object": true, "array": true,
	"number": true, "integer": true, "string": true,
}

// Compile parses a schema document. It refuses keywords outside the
// supported subset (see the package doc) and $refs it cannot resolve.
func Compile(b []byte) (*Schema, error) {
	doc, err := decode(b)
	if err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	c := &compiler{defs: map[string]*node{}}
	if o, ok := doc.(*object); ok {
		if d, ok := o.m["$defs"]; ok {
			do, ok := d.(*object)
			if !ok {
				return nil, errors.New("schema: $defs must be an object")
			}
			// Make every def first so refs between defs resolve.
			for _, k := range do.keys {
				c.defs[k] = &node{}
			}
			for _, k := range do.keys {
				n, err := c.node(do.m[k], "/$defs/"+k)
				if err != nil {
					return nil, err
				}
				*c.defs[k] = *n
			}
		}
	}
	root, err := c.node(doc, "")
	if err != nil {
		return nil, err
	}
	for _, r := range c.refs {
		if c.defs[r] == nil {
			return nil, fmt.Errorf("schema: $ref %q: only #/$defs/<name> refs to an existing def are supported", "#/$defs/"+r)
		}
	}
	return &Schema{root: root, defs: c.defs}, nil
}

type compiler struct {
	defs map[string]*node
	refs []string
}

func (c *compiler) node(v any, at string) (*node, error) {
	if b, ok := v.(bool); ok {
		return &node{always: &b}, nil
	}
	o, ok := v.(*object)
	if !ok {
		return nil, fmt.Errorf("schema %s: a schema must be an object or a boolean", at)
	}
	n := &node{maxProps: -1, maxItems: -1, maxLen: -1}
	bad := func(k, why string) error { return fmt.Errorf("schema %s/%s: %s", at, k, why) }
	for _, k := range o.keys {
		val := o.m[k]
		var err error
		switch {
		case annotations[k]:
		case k == "$defs":
			if at != "" {
				return nil, bad(k, "$defs is supported only at the root")
			}
		case k == "$ref":
			s, ok := val.(string)
			if !ok {
				return nil, bad(k, "must be a string")
			}
			name, ok := strings.CutPrefix(s, "#/$defs/")
			if !ok || name == "" {
				return nil, bad(k, fmt.Sprintf("%q: only #/$defs/<name> refs are supported", s))
			}
			n.ref = name
			c.refs = append(c.refs, name)
		case k == "type":
			switch t := val.(type) {
			case string:
				n.types = []string{t}
			case []any:
				for _, e := range t {
					s, ok := e.(string)
					if !ok {
						return nil, bad(k, "must list strings")
					}
					n.types = append(n.types, s)
				}
			default:
				return nil, bad(k, "must be a string or an array")
			}
			for _, t := range n.types {
				if !knownTypes[t] {
					return nil, bad(k, fmt.Sprintf("unknown type %q", t))
				}
			}
		case k == "enum":
			a, ok := val.([]any)
			if !ok || len(a) == 0 {
				return nil, bad(k, "must be a non-empty array")
			}
			n.enum = a
		case k == "const":
			n.hasConst, n.constV = true, val
		case k == "not":
			n.not, err = c.node(val, at+"/not")
		case k == "properties":
			po, ok := val.(*object)
			if !ok {
				return nil, bad(k, "must be an object")
			}
			n.props = map[string]*node{}
			for _, pk := range po.keys {
				if n.props[pk], err = c.node(po.m[pk], at+"/properties/"+pk); err != nil {
					return nil, err
				}
			}
		case k == "required":
			a, ok := val.([]any)
			if !ok {
				return nil, bad(k, "must be an array")
			}
			for _, e := range a {
				s, ok := e.(string)
				if !ok {
					return nil, bad(k, "must list strings")
				}
				n.required = append(n.required, s)
			}
		case k == "additionalProperties":
			n.addl, err = c.node(val, at+"/additionalProperties")
		case k == "propertyNames":
			n.propertyNames, err = c.node(val, at+"/propertyNames")
		case k == "items":
			n.items, err = c.node(val, at+"/items")
		case k == "uniqueItems":
			b, ok := val.(bool)
			if !ok {
				return nil, bad(k, "must be a boolean")
			}
			n.unique = b
		case k == "pattern":
			s, ok := val.(string)
			if !ok {
				return nil, bad(k, "must be a string")
			}
			if n.pattern, err = regexp.Compile(s); err != nil {
				return nil, bad(k, err.Error())
			}
		case k == "minLength", k == "maxLength", k == "minItems", k == "maxItems", k == "minProperties", k == "maxProperties":
			num, ok := val.(json.Number)
			i, ierr := strconv.Atoi(string(num))
			if !ok || ierr != nil || i < 0 {
				return nil, bad(k, "must be a non-negative integer")
			}
			*map[string]*int{
				"minLength": &n.minLen, "maxLength": &n.maxLen, "minItems": &n.minItems,
				"maxItems": &n.maxItems, "minProperties": &n.minProps, "maxProperties": &n.maxProps,
			}[k] = i
		case k == "minimum", k == "maximum":
			num, ok := val.(json.Number)
			r, rok := new(big.Rat).SetString(string(num))
			if !ok || !rok {
				return nil, bad(k, "must be a number")
			}
			if k == "minimum" {
				n.minimum = r
			} else {
				n.maximum = r
			}
		default:
			return nil, bad(k, "keyword not supported by this validator")
		}
		if err != nil {
			return nil, err
		}
	}
	return n, nil
}

// ValidationError lists where a document breaks its schema.
type ValidationError struct {
	Problems []string // "<JSON pointer>: <what is wrong>", in document order
}

func (e *ValidationError) Error() string {
	return "does not match the schema: " + strings.Join(e.Problems, "; ")
}

// maxProblems bounds how many problems one Validate reports.
const maxProblems = 20

// maxRefDepth bounds schema recursion (a $ref cycle that does not
// descend into the document).
const maxRefDepth = 256

// Validate checks one JSON document. A parse failure is returned as is;
// a schema mismatch is a *ValidationError.
func (s *Schema) Validate(doc []byte) error {
	v, err := decode(doc)
	if err != nil {
		return err
	}
	vr := &validator{defs: s.defs}
	vr.check(s.root, v, "", 0)
	if len(vr.problems) > 0 {
		return &ValidationError{Problems: vr.problems}
	}
	return nil
}

type validator struct {
	defs     map[string]*node
	problems []string
	quiet    int // >0 inside "not": record failures without reporting
	failed   bool
}

func (vr *validator) fail(at, format string, args ...any) {
	vr.failed = true
	if vr.quiet > 0 || len(vr.problems) >= maxProblems {
		return
	}
	if at == "" {
		at = "/"
	}
	vr.problems = append(vr.problems, at+": "+fmt.Sprintf(format, args...))
}

func pointer(at, key string) string {
	key = strings.ReplaceAll(key, "~", "~0")
	return at + "/" + strings.ReplaceAll(key, "/", "~1")
}

func (vr *validator) check(n *node, v any, at string, depth int) {
	if depth > maxRefDepth {
		vr.fail(at, "schema nests deeper than %d", maxRefDepth)
		return
	}
	if n.always != nil {
		if !*n.always {
			vr.fail(at, "no value is allowed here")
		}
		return
	}
	if n.ref != "" {
		vr.check(vr.defs[n.ref], v, at, depth+1)
	}
	if len(n.types) > 0 && !typeMatches(n.types, v) {
		vr.fail(at, "is %s, want %s", typeName(v), strings.Join(n.types, " or "))
		return
	}
	if n.enum != nil {
		ok := false
		for _, e := range n.enum {
			if canon(e) == canon(v) {
				ok = true
				break
			}
		}
		if !ok {
			vr.fail(at, "is %s, want one of %s", short(v), enumList(n.enum))
		}
	}
	if n.hasConst && canon(n.constV) != canon(v) {
		vr.fail(at, "is %s, want %s", short(v), short(n.constV))
	}
	if n.not != nil {
		sub := &validator{defs: vr.defs, quiet: 1}
		sub.check(n.not, v, at, depth+1)
		if !sub.failed {
			vr.fail(at, "%s is not allowed", short(v))
		}
	}
	switch t := v.(type) {
	case *object:
		vr.object(n, t, at, depth)
	case []any:
		if len(t) < n.minItems {
			vr.fail(at, "has %d items, want at least %d", len(t), n.minItems)
		}
		if n.maxItems >= 0 && len(t) > n.maxItems {
			vr.fail(at, "has %d items, want at most %d", len(t), n.maxItems)
		}
		seen := map[string]int{}
		for i, e := range t {
			ep := at + "/" + strconv.Itoa(i)
			if n.items != nil {
				vr.check(n.items, e, ep, depth+1)
			}
			if n.unique {
				k := canon(e)
				if j, dup := seen[k]; dup {
					vr.fail(ep, "repeats item %d", j)
				}
				seen[k] = i
			}
		}
	case string:
		l := utf8.RuneCountInString(t)
		if l < n.minLen {
			vr.fail(at, "is %d characters, want at least %d", l, n.minLen)
		}
		if n.maxLen >= 0 && l > n.maxLen {
			vr.fail(at, "is %d characters, want at most %d", l, n.maxLen)
		}
		if n.pattern != nil && !n.pattern.MatchString(t) {
			vr.fail(at, "%s does not match %s", short(t), n.pattern)
		}
	case json.Number:
		r, ok := new(big.Rat).SetString(string(t))
		if !ok {
			vr.fail(at, "%s is not a number", t)
			return
		}
		if n.minimum != nil && r.Cmp(n.minimum) < 0 {
			vr.fail(at, "is %s, want at least %s", t, n.minimum.RatString())
		}
		if n.maximum != nil && r.Cmp(n.maximum) > 0 {
			vr.fail(at, "is %s, want at most %s", t, n.maximum.RatString())
		}
	}
}

func (vr *validator) object(n *node, o *object, at string, depth int) {
	if len(o.keys) < n.minProps {
		vr.fail(at, "has %d properties, want at least %d", len(o.keys), n.minProps)
	}
	if n.maxProps >= 0 && len(o.keys) > n.maxProps {
		vr.fail(at, "has %d properties, want at most %d", len(o.keys), n.maxProps)
	}
	for _, r := range n.required {
		if _, ok := o.m[r]; !ok {
			vr.fail(at, "missing required property %q", r)
		}
	}
	for _, k := range o.keys {
		kp := pointer(at, k)
		if n.propertyNames != nil {
			vr.check(n.propertyNames, k, kp, depth+1)
		}
		if p, ok := n.props[k]; ok {
			vr.check(p, o.m[k], kp, depth+1)
			continue
		}
		if n.addl != nil {
			if n.addl.always != nil && !*n.addl.always {
				vr.fail(kp, "unknown property")
				continue
			}
			vr.check(n.addl, o.m[k], kp, depth+1)
		}
	}
}

func typeMatches(types []string, v any) bool {
	got := typeName(v)
	for _, t := range types {
		if t == got {
			return true
		}
		if t == "integer" && got == "number" && isInteger(v.(json.Number)) {
			return true
		}
		if t == "number" && got == "number" {
			return true
		}
	}
	return false
}

// short renders a value for a message, cut to 60 bytes.
func short(v any) string {
	var s string
	switch t := v.(type) {
	case string:
		b, _ := json.Marshal(t)
		s = string(b)
	case json.Number:
		s = t.String()
	case nil:
		s = "null"
	case bool:
		s = strconv.FormatBool(t)
	default:
		s = typeName(v)
	}
	if len(s) > 60 {
		s = s[:57] + "..."
	}
	return s
}

func enumList(e []any) string {
	parts := make([]string, len(e))
	for i, v := range e {
		parts[i] = short(v)
	}
	return strings.Join(parts, ", ")
}

func sortStrings(s []string) { slices.Sort(s) }
