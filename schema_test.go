package surf

import (
	"reflect"
	"testing"
	"time"
)

type schemaAddr struct {
	City string `json:"city"`
	Zip  string `json:"zip,omitempty"`
}

type schemaUser struct {
	ID       string         `json:"id" desc:"Unique identifier"`
	Name     string         `json:"name" required:"true"`
	Age      int            `json:"age,omitempty"`
	Nickname *string        `json:"nickname,omitempty"`
	Tags     []string       `json:"tags"`
	Address  schemaAddr     `json:"address"`
	Created  time.Time      `json:"created"`
	Meta     map[string]int `json:"meta,omitempty"`
	secret   string         // unexported, ignored
	Skip     string         `json:"-"`
}

func TestSchemaForPrimitives(t *testing.T) {
	cases := []struct {
		v        any
		wantType string
		wantFmt  string
	}{
		{true, "boolean", ""},
		{int64(0), "integer", ""},
		{3.14, "number", ""},
		{"s", "string", ""},
		{time.Time{}, "string", "date-time"},
		{[]byte(nil), "string", "byte"},
	}
	for _, c := range cases {
		s := SchemaFor(reflect.TypeOf(c.v))
		if s.Type != c.wantType || s.Format != c.wantFmt {
			t.Errorf("SchemaFor(%T) = {%q,%q}, want {%q,%q}", c.v, s.Type, s.Format, c.wantType, c.wantFmt)
		}
	}
}

func TestSchemaForNil(t *testing.T) {
	if SchemaFor(nil) != nil {
		t.Fatal("SchemaFor(nil) should be nil")
	}
}

func TestSchemaForStruct(t *testing.T) {
	s := SchemaFor(reflect.TypeOf(schemaUser{}))

	// Root is a $ref into $defs for a named struct.
	if s.Ref != "#/$defs/schemaUser" {
		t.Fatalf("root ref = %q, want #/$defs/schemaUser", s.Ref)
	}
	def := s.Defs["schemaUser"]
	if def == nil {
		t.Fatal("schemaUser not in $defs")
	}

	// Unexported and json:"-" fields are excluded.
	if _, ok := def.Properties["secret"]; ok {
		t.Error("unexported field leaked into schema")
	}
	if _, ok := def.Properties["Skip"]; ok {
		t.Error("json:\"-\" field leaked into schema")
	}

	// desc tag becomes description.
	if got := def.Properties["id"].Description; got != "Unique identifier" {
		t.Errorf("id description = %q", got)
	}

	// Nested named struct is a $ref and appears in $defs.
	if def.Properties["address"].Ref != "#/$defs/schemaAddr" {
		t.Errorf("address ref = %q", def.Properties["address"].Ref)
	}
	if s.Defs["schemaAddr"] == nil {
		t.Error("schemaAddr not registered in $defs")
	}

	// time.Time special-cased even as a field.
	if c := def.Properties["created"]; c.Type != "string" || c.Format != "date-time" {
		t.Errorf("created = {%q,%q}, want string/date-time", c.Type, c.Format)
	}

	// slice and map.
	if def.Properties["tags"].Type != "array" || def.Properties["tags"].Items.Type != "string" {
		t.Error("tags should be array of string")
	}
	if def.Properties["meta"].Type != "object" || def.Properties["meta"].AdditionalProperties.Type != "integer" {
		t.Error("meta should be object with integer additionalProperties")
	}
}

func TestSchemaRequired(t *testing.T) {
	def := SchemaFor(reflect.TypeOf(schemaUser{})).Defs["schemaUser"]
	req := map[string]bool{}
	for _, r := range def.Required {
		req[r] = true
	}

	// required:"true" forces it; non-pointer non-omitempty defaults required.
	if !req["name"] {
		t.Error("name should be required (required:\"true\")")
	}
	if !req["id"] {
		t.Error("id should be required (non-pointer, no omitempty)")
	}
	if !req["tags"] {
		t.Error("tags should be required (no omitempty)")
	}
	// omitempty and pointer fields are optional.
	if req["age"] {
		t.Error("age should be optional (omitempty)")
	}
	if req["nickname"] {
		t.Error("nickname should be optional (pointer)")
	}
}

type schemaNode struct {
	Value    int           `json:"value"`
	Children []*schemaNode `json:"children,omitempty"`
}

func TestSchemaRecursive(t *testing.T) {
	// A self-referential type must terminate via $ref, not recurse forever.
	s := SchemaFor(reflect.TypeOf(schemaNode{}))
	def := s.Defs["schemaNode"]
	if def == nil {
		t.Fatal("schemaNode missing from $defs")
	}
	if def.Properties["children"].Items.Ref != "#/$defs/schemaNode" {
		t.Errorf("children items ref = %q, want self-ref", def.Properties["children"].Items.Ref)
	}
}
