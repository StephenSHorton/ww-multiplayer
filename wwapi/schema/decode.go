package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// object is a decoded JSON object that keeps its key order.
type object struct {
	keys []string
	m    map[string]any
}

// maxDepth bounds nesting so a hostile document cannot exhaust the stack.
const maxDepth = 64

// Decode parses one JSON document into nil, bool, json.Number, string,
// []any or *object values. It refuses duplicate keys, nesting deeper than
// 64 and anything after the document.
func decode(b []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	v, err := decodeValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON document")
	}
	return v, nil
}

func decodeValue(d *json.Decoder, depth int) (any, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("nested deeper than %d", maxDepth)
	}
	tok, err := d.Token()
	if err != nil {
		if err == io.EOF {
			return nil, errors.New("unexpected end of JSON")
		}
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := &object{m: map[string]any{}}
			for d.More() {
				kt, err := d.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, fmt.Errorf("object key %v is not a string", kt)
				}
				if _, dup := o.m[k]; dup {
					return nil, fmt.Errorf("duplicate key %q", k)
				}
				v, err := decodeValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				o.keys = append(o.keys, k)
				o.m[k] = v
			}
			if _, err := d.Token(); err != nil {
				return nil, err
			}
			return o, nil
		case '[':
			a := []any{}
			for d.More() {
				v, err := decodeValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			if _, err := d.Token(); err != nil {
				return nil, err
			}
			return a, nil
		}
		return nil, fmt.Errorf("unexpected %v", t)
	default:
		return t, nil // nil, bool, json.Number, string
	}
}

// typeName is the JSON Schema type of a decoded value. Numbers report
// "number"; isInteger tells integers apart.
func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case *object:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

func isInteger(n json.Number) bool {
	_, err := n.Int64()
	return err == nil
}

// canon is a canonical key for scalar equality (enum, const,
// uniqueItems). Numbers compare by value when they fit a float64 exactly
// enough for manifests; containers compare by their canonical JSON.
func canon(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		if t {
			return "true"
		}
		return "false"
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return fmt.Sprintf("n:%v", f)
		}
		return "n:" + t.String()
	case string:
		b, _ := json.Marshal(t)
		return string(b)
	case []any:
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			buf.WriteString(canon(e))
		}
		buf.WriteByte(']')
		return buf.String()
	case *object:
		// Key order does not matter for equality.
		keys := append([]string(nil), t.keys...)
		sortStrings(keys)
		var buf bytes.Buffer
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteByte(':')
			buf.WriteString(canon(t.m[k]))
		}
		buf.WriteByte('}')
		return buf.String()
	}
	return fmt.Sprintf("%v", v)
}
