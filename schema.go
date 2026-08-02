package surf

import (
	"reflect"
	"strings"
	"time"
)

// Schema is a minimal JSON Schema (draft 2020-12 / OpenAPI 3.1 compatible)
// node. It describes handler request and response types and MCP tool input
// schemas. It is produced by reflection over Go types at registration time —
// never per request — so building it is a one-time cost that all three
// introspection consumers (OpenAPI, per-route spec negotiation, MCP) share.
type Schema struct {
	Type                 string             `json:"type,omitempty"`
	Format               string             `json:"format,omitempty"`
	Description          string             `json:"description,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	AdditionalProperties *Schema            `json:"additionalProperties,omitempty"`
	Ref                  string             `json:"$ref,omitempty"`
	Defs                 map[string]*Schema `json:"$defs,omitempty"`
}

// timeType is special-cased to a string/date-time schema rather than being
// walked as a struct.
var timeType = reflect.TypeOf(time.Time{})

// schemaBuilder walks Go types into Schema nodes. Named struct types are
// registered in Defs and referenced with $ref, which both deduplicates repeated
// types and terminates recursion on self-referential structs.
type schemaBuilder struct {
	// Defs holds every named struct schema, keyed by its Go type name. The
	// caller decides where these land ("#/components/schemas/" for OpenAPI,
	// "#/$defs/" for a standalone JSON Schema).
	Defs    map[string]*Schema
	refBase string
	seen    map[reflect.Type]bool
}

// newSchemaBuilder returns a builder whose $ref pointers are prefixed with
// refBase (e.g. "#/components/schemas/").
func newSchemaBuilder(refBase string) *schemaBuilder {
	return &schemaBuilder{
		Defs:    make(map[string]*Schema),
		refBase: refBase,
		seen:    make(map[reflect.Type]bool),
	}
}

// SchemaFor builds a self-contained JSON Schema for t. Named nested structs are
// collected into the returned schema's "$defs" and referenced with $ref, which
// both deduplicates repeated types and terminates recursion. It returns nil for
// a nil type, so callers can pass a route's ReqType/RespType directly without a
// guard. This is the form MCP tool inputSchemas and per-route spec negotiation
// use; OpenAPIDoc threads the same builder's Defs through components.schemas.
func SchemaFor(t reflect.Type) *Schema {
	if t == nil {
		return nil
	}
	b := newSchemaBuilder("#/$defs/")
	s := b.build(t)
	if s == nil {
		return nil
	}
	if len(b.Defs) > 0 {
		s.Defs = b.Defs
	}
	return s
}

// build converts a single Go type into a Schema node.
func (b *schemaBuilder) build(t reflect.Type) *Schema {
	// Unwrap pointers; nullability is expressed by omission from "required".
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	if t == timeType {
		return &Schema{Type: "string", Format: "date-time"}
	}

	switch t.Kind() {
	case reflect.Bool:
		return &Schema{Type: "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &Schema{Type: "integer"}
	case reflect.Float32, reflect.Float64:
		return &Schema{Type: "number"}
	case reflect.String:
		return &Schema{Type: "string"}
	case reflect.Slice, reflect.Array:
		// []byte encodes as a base64 string in encoding/json.
		if t.Elem().Kind() == reflect.Uint8 {
			return &Schema{Type: "string", Format: "byte"}
		}
		return &Schema{Type: "array", Items: b.build(t.Elem())}
	case reflect.Map:
		return &Schema{Type: "object", AdditionalProperties: b.build(t.Elem())}
	case reflect.Struct:
		return b.buildStruct(t)
	case reflect.Interface:
		// any / interface{}: unconstrained.
		return &Schema{}
	default:
		return &Schema{}
	}
}

// buildStruct registers t in Defs (if named) and returns a $ref to it; for
// anonymous structs it returns the object schema inline.
func (b *schemaBuilder) buildStruct(t reflect.Type) *Schema {
	name := t.Name()
	if name != "" {
		ref := &Schema{Ref: b.refBase + name}
		if b.seen[t] {
			return ref
		}
		b.seen[t] = true
		b.Defs[name] = b.objectSchema(t)
		return ref
	}
	return b.objectSchema(t)
}

// objectSchema builds the properties/required for a struct type. It honors the
// framework's tag vocabulary: `json` for property names (`-` skips, `omitempty`
// implies optional), `desc` for a property description, and `required:"true"`
// to force a field into the required set.
func (b *schemaBuilder) objectSchema(t reflect.Type) *Schema {
	s := &Schema{Type: "object", Properties: map[string]*Schema{}}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)

		if !f.IsExported() {
			continue
		}

		name, omitempty, skip := jsonFieldName(f)
		if skip {
			continue
		}
		// Promote embedded (anonymous) struct fields inline.
		if f.Anonymous && name == "" {
			ft := f.Type
			for ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				embedded := b.objectSchema(ft)
				for k, v := range embedded.Properties {
					s.Properties[k] = v
				}
				s.Required = append(s.Required, embedded.Required...)
				continue
			}
		}
		if name == "" {
			name = f.Name
		}

		prop := b.build(f.Type)
		if d := f.Tag.Get("desc"); d != "" {
			prop.Description = d
		}
		s.Properties[name] = prop

		if isRequired(f, omitempty) {
			s.Required = append(s.Required, name)
		}
	}
	if len(s.Properties) == 0 {
		s.Properties = nil
	}
	return s
}

// jsonFieldName parses the `json` struct tag, returning the wire name, whether
// omitempty is set, and whether the field is skipped (`json:"-"`).
func jsonFieldName(f reflect.StructField) (name string, omitempty, skip bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", false, true
	}
	if tag == "" {
		return "", false, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty, false
}

// isRequired decides whether a field belongs in the schema's required set.
// An explicit `required:"true"`/`required:"false"` tag wins; otherwise a field
// is required when it is a non-pointer whose json tag has no omitempty.
func isRequired(f reflect.StructField, omitempty bool) bool {
	switch strings.ToLower(f.Tag.Get("required")) {
	case "true":
		return true
	case "false":
		return false
	}
	return f.Type.Kind() != reflect.Pointer && !omitempty
}
