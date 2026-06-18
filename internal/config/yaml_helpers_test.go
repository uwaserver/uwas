package config

import (
	"reflect"
	"testing"
)

// Local struct types to drive the reflect-based YAML helpers through branches
// the concrete Domain struct does not exercise (no-tag fields, name "-",
// empty-name tag, unexported fields, pointer-to-struct entry).
type yamlHelperSample struct {
	NoTag      string `` // no yaml tag -> lowercased field name
	Dash       string `yaml:"-"`
	EmptyName  string `yaml:",omitempty"`        // empty name -> lowercased field name
	Normal     string `yaml:"normal,omitempty"`  //
	Skipped    string `yaml:"skipped,omitempty"` // empty + omitempty -> dropped
	unexported string `yaml:"unexp"`             // unexported -> skipped
}

func TestYamlFieldName_Branches(t *testing.T) {
	tt := reflect.TypeOf(yamlHelperSample{})

	name, omit := yamlFieldName(tt.Field(0)) // NoTag
	if name != "notag" || omit {
		t.Errorf("NoTag: got (%q,%v)", name, omit)
	}
	name, _ = yamlFieldName(tt.Field(1)) // Dash
	if name != "-" {
		t.Errorf("Dash: got %q", name)
	}
	name, omit = yamlFieldName(tt.Field(2)) // EmptyName
	if name != "emptyname" || !omit {
		t.Errorf("EmptyName: got (%q,%v)", name, omit)
	}
}

func TestYamlMapFromStruct_SkipsAndDrops(t *testing.T) {
	s := yamlHelperSample{NoTag: "x", Dash: "d", EmptyName: "e", Normal: "n", unexported: "u"}
	out, err := yamlMapFromStruct(reflect.ValueOf(s), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["-"]; ok {
		t.Error("dash field should be skipped")
	}
	if _, ok := out["unexp"]; ok {
		t.Error("unexported field should be skipped")
	}
	if _, ok := out["skipped"]; ok {
		t.Error("empty+omitempty field should be dropped")
	}
	if out["notag"] != "x" || out["normal"] != "n" || out["emptyname"] != "e" {
		t.Errorf("unexpected map: %+v", out)
	}
}

// Pointer-to-struct entry: non-nil pointer is dereferenced.
func TestYamlMapFromStruct_PointerEntry(t *testing.T) {
	s := &yamlHelperSample{Normal: "p"}
	out, err := yamlMapFromStruct(reflect.ValueOf(s), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out["normal"] != "p" {
		t.Errorf("pointer-entry deref failed: %+v", out)
	}
}

// Nil pointer entry returns (nil, nil).
func TestYamlMapFromStruct_NilPointerEntry(t *testing.T) {
	var s *yamlHelperSample
	out, err := yamlMapFromStruct(reflect.ValueOf(s), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		t.Errorf("nil pointer entry should yield nil map, got %+v", out)
	}
}

// yamlValue branches: invalid value, nil pointer, struct-without-marshaler,
// nested slice and map of plain values.
func TestYamlValue_Branches(t *testing.T) {
	// invalid value
	if v, err := yamlValue(reflect.Value{}); err != nil || v != nil {
		t.Errorf("invalid value: got (%v,%v)", v, err)
	}
	// nil pointer
	var p *int
	if v, err := yamlValue(reflect.ValueOf(p)); err != nil || v != nil {
		t.Errorf("nil pointer: got (%v,%v)", v, err)
	}
	// non-nil pointer to plain int (no marshaler) -> deref
	n := 42
	if v, err := yamlValue(reflect.ValueOf(&n)); err != nil || v.(int) != 42 {
		t.Errorf("pointer deref: got (%v,%v)", v, err)
	}
	// plain struct (no marshaler)
	type plain struct {
		A string `yaml:"a"`
	}
	v, err := yamlValue(reflect.ValueOf(plain{A: "z"}))
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := v.(map[string]any); !ok || m["a"] != "z" {
		t.Errorf("plain struct: got %T %v", v, v)
	}
	// slice of plain values
	v, err = yamlValue(reflect.ValueOf([]int{1, 2, 3}))
	if err != nil {
		t.Fatal(err)
	}
	if items, ok := v.([]any); !ok || len(items) != 3 {
		t.Errorf("slice: got %T %v", v, v)
	}
	// map of plain values
	v, err = yamlValue(reflect.ValueOf(map[string]int{"k": 9}))
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := v.(map[any]any); !ok || m["k"].(int) != 9 {
		t.Errorf("map: got %T %v", v, v)
	}
	// nil map -> nil
	var nm map[string]int
	if v, err := yamlValue(reflect.ValueOf(nm)); err != nil || v != nil {
		t.Errorf("nil map: got (%v,%v)", v, err)
	}
}

// yamlValue must use a value-receiver custom marshaler via the Addr() seam.
func TestYamlValue_AddressableMarshaler(t *testing.T) {
	// ByteSize has a value-receiver MarshalYAML; wrap in a struct field so it is
	// addressable and routed through the v.Addr() marshaler branch.
	bs := 2 * GB
	v, err := yamlValue(reflect.ValueOf(bs))
	if err != nil {
		t.Fatal(err)
	}
	if v != "2GB" {
		t.Errorf("ByteSize marshal via yamlValue: got %v", v)
	}
}

// yamlEmpty covers the numeric / bool / float / invalid cases.
func TestYamlEmpty_Kinds(t *testing.T) {
	cases := []struct {
		name string
		val  any
		want bool
	}{
		{"zero int", int(0), true},
		{"nonzero int", int(5), false},
		{"zero uint", uint(0), true},
		{"nonzero uint", uint(5), false},
		{"zero float", float64(0), true},
		{"nonzero float", float64(1.5), false},
		{"false bool", false, true},
		{"true bool", true, false},
		{"empty string", "", true},
		{"nonempty string", "x", false},
		{"empty slice", []int{}, true},
		{"nonempty slice", []int{1}, false},
		{"zero struct", struct{ A int }{}, true},
		{"nonzero struct", struct{ A int }{A: 1}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := yamlEmpty(reflect.ValueOf(c.val)); got != c.want {
				t.Errorf("yamlEmpty(%v)=%v want %v", c.val, got, c.want)
			}
		})
	}
	// invalid value
	if !yamlEmpty(reflect.Value{}) {
		t.Error("invalid value should be empty")
	}
	// nil pointer
	var p *int
	if !yamlEmpty(reflect.ValueOf(p)) {
		t.Error("nil pointer should be empty")
	}
}
