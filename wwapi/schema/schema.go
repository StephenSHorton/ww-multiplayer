// Package schema holds the mod manifest's JSON Schema (mod.schema.json)
// and the small JSON Schema validator that checks manifests against it.
//
// # v0 UNSTABLE
//
// This package is part of wwapi v0. Its API may change in any 0.x minor
// version.
//
// The validator implements the subset of JSON Schema draft 2020-12 that
// mod.schema.json uses, so the module needs no third-party dependency:
//
//	type  enum  const  not  $ref ("#/$defs/<name>" only)  $defs
//	properties  required  additionalProperties  propertyNames
//	minProperties  maxProperties
//	items  minItems  maxItems  uniqueItems
//	pattern (Go RE2 syntax)  minLength  maxLength
//	minimum  maximum
//
// plus the annotations $schema, $id, $comment, title, description, default
// and examples, which it ignores. [Compile] refuses a schema with any
// other keyword, so the schema can never use a rule the validator would
// silently skip. Two choices are stricter than the spec: an "integer" is
// a number written without a fraction or exponent, and a document with a
// duplicate object key is refused.
package schema

import (
	_ "embed"
	"sync"
)

//go:embed mod.schema.json
var modSchema []byte

// ModJSON returns the text of mod.schema.json.
func ModJSON() []byte { return append([]byte(nil), modSchema...) }

var modOnce = sync.OnceValues(func() (*Schema, error) { return Compile(modSchema) })

// Mod returns the compiled mod.schema.json. It panics if the embedded
// schema does not compile, which the package's tests rule out.
func Mod() *Schema {
	s, err := modOnce()
	if err != nil {
		panic("wwapi/schema: mod.schema.json: " + err.Error())
	}
	return s
}
