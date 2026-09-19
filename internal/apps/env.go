package apps

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// EnvMap is an ordered string→string map. Go's native map loses key order
// on YAML/JSON marshal (keys are sorted), which reshuffles the operator's
// Environment textarea on every save. EnvMap keeps insertion order for
// disk + API round-trips while still exposing a plain map for process env.
type EnvMap struct {
	order []string
	m     map[string]string
}

// EnvFromMap builds an EnvMap from a plain map. Iteration order of m is
// undefined, so callers that care about order should use EnvFromPairs or
// Unmarshal instead.
func EnvFromMap(m map[string]string) *EnvMap {
	if len(m) == 0 {
		if m == nil {
			return nil
		}
		return &EnvMap{order: []string{}, m: map[string]string{}}
	}
	e := &EnvMap{order: make([]string, 0, len(m)), m: make(map[string]string, len(m))}
	for k, v := range m {
		e.order = append(e.order, k)
		e.m[k] = v
	}
	return e
}

// EnvFromPairs builds an EnvMap from alternating key, value strings.
// Odd trailing key is ignored. Duplicate keys keep the last value and
// their first-seen position.
func EnvFromPairs(kv ...string) *EnvMap {
	if len(kv) < 2 {
		return nil
	}
	e := &EnvMap{m: make(map[string]string, len(kv)/2)}
	for i := 0; i+1 < len(kv); i += 2 {
		e.Set(kv[i], kv[i+1])
	}
	if e.Len() == 0 {
		return nil
	}
	return e
}

// Get returns the value for k, or "" if missing / e is nil.
func (e *EnvMap) Get(k string) string {
	if e == nil || e.m == nil {
		return ""
	}
	return e.m[k]
}

// Set inserts or updates k. New keys append; updates keep position.
func (e *EnvMap) Set(k, v string) {
	if e == nil {
		return
	}
	if e.m == nil {
		e.m = make(map[string]string)
	}
	if _, ok := e.m[k]; !ok {
		e.order = append(e.order, k)
	}
	e.m[k] = v
}

// Len returns the number of entries.
func (e *EnvMap) Len() int {
	if e == nil {
		return 0
	}
	return len(e.m)
}

// Map returns a plain copy (order not preserved on further map ops).
func (e *EnvMap) Map() map[string]string {
	if e == nil || len(e.m) == 0 {
		return nil
	}
	out := make(map[string]string, len(e.m))
	for k, v := range e.m {
		out[k] = v
	}
	return out
}

// Clone returns a deep copy. nil stays nil.
func (e *EnvMap) Clone() *EnvMap {
	if e == nil {
		return nil
	}
	out := &EnvMap{
		order: append([]string(nil), e.order...),
		m:     make(map[string]string, len(e.m)),
	}
	for k, v := range e.m {
		out.m[k] = v
	}
	return out
}

// Range calls fn for each pair in insertion order. Stop early by returning false.
func (e *EnvMap) Range(fn func(k, v string) bool) {
	if e == nil {
		return
	}
	for _, k := range e.order {
		v, ok := e.m[k]
		if !ok {
			continue
		}
		if !fn(k, v) {
			return
		}
	}
}

// MarshalJSON encodes as a JSON object with keys in insertion order.
func (e *EnvMap) MarshalJSON() ([]byte, error) {
	if e == nil || len(e.m) == 0 {
		return []byte("null"), nil
	}
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	for _, k := range e.order {
		v, ok := e.m[k]
		if !ok {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		vb, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(vb)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// UnmarshalJSON decodes a JSON object, preserving key order from the payload.
func (e *EnvMap) UnmarshalJSON(data []byte) error {
	if e == nil {
		return fmt.Errorf("apps: UnmarshalJSON on nil EnvMap")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		e.order = nil
		e.m = nil
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok || d != '{' {
		return fmt.Errorf("apps: env: expected JSON object")
	}
	m := make(map[string]string)
	order := make([]string, 0, 8)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("apps: env: expected string key")
		}
		var val string
		if err := dec.Decode(&val); err != nil {
			return fmt.Errorf("apps: env: decode value for %q: %w", key, err)
		}
		if _, exists := m[key]; !exists {
			order = append(order, key)
		}
		m[key] = val
	}
	tok, err = dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '}' {
		return fmt.Errorf("apps: env: expected end of object")
	}
	e.order = order
	e.m = m
	return nil
}

// MarshalYAML encodes as a mapping node with keys in insertion order.
func (e EnvMap) MarshalYAML() (interface{}, error) {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if e.m == nil {
		return node, nil
	}
	for _, k := range e.order {
		v, ok := e.m[k]
		if !ok {
			continue
		}
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v},
		)
	}
	return node, nil
}

// UnmarshalYAML decodes a YAML mapping, preserving document key order.
func (e *EnvMap) UnmarshalYAML(value *yaml.Node) error {
	if e == nil {
		return fmt.Errorf("apps: UnmarshalYAML on nil EnvMap")
	}
	if value == nil || value.Kind == yaml.ScalarNode && (value.Tag == "!!null" || value.Value == "null" || value.Value == "~") {
		e.order = nil
		e.m = nil
		return nil
	}
	if value.Kind != yaml.MappingNode {
		return fmt.Errorf("apps: env: expected YAML mapping")
	}
	m := make(map[string]string, len(value.Content)/2)
	order := make([]string, 0, len(value.Content)/2)
	for i := 0; i+1 < len(value.Content); i += 2 {
		k := value.Content[i].Value
		v := value.Content[i+1].Value
		if _, exists := m[k]; !exists {
			order = append(order, k)
		}
		m[k] = v
	}
	e.order = order
	e.m = m
	return nil
}
