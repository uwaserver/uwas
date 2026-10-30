package config

import (
	"bytes"
	"strings"

	"gopkg.in/yaml.v3"
)

// PreserveEnvReferences re-inserts the ${VAR} references the operator wrote.
//
// Load expands ${VAR} and ${VAR:-fallback} in the YAML text before parsing, so
// the live configuration holds the values. Saving that configuration back to
// the file replaced every reference with the value it had expanded to: a
// secret that was only meant to live in the environment (an API key, an S3
// secret, a webhook secret) was written to disk, and changing the environment
// variable afterwards no longer had any effect.
//
// previous is the file as it is on disk now and written is the YAML about to
// replace it. A scalar of previous that contains a reference is put back at
// the same key path of written when written still holds what the reference
// expands to; a value the operator changed keeps its new value. Anything that
// cannot be parsed or matched leaves written as it is.
func PreserveEnvReferences(previous, written []byte) []byte {
	previous = stripBOM(previous)
	if !bytes.Contains(previous, []byte("${")) {
		return written
	}
	var oldDoc, newDoc yaml.Node
	if yaml.Unmarshal(previous, &oldDoc) != nil || yaml.Unmarshal(written, &newDoc) != nil {
		return written
	}
	if !restoreEnvRefs(&oldDoc, &newDoc) {
		return written
	}
	out, err := yaml.Marshal(&newDoc)
	if err != nil {
		return written
	}
	return out
}

// restoreEnvRefs walks both trees by key path and reports whether it restored
// at least one reference.
func restoreEnvRefs(old, cur *yaml.Node) bool {
	if old == nil || cur == nil {
		return false
	}
	if old.Kind == yaml.DocumentNode && len(old.Content) > 0 {
		old = old.Content[0]
	}
	if cur.Kind == yaml.DocumentNode && len(cur.Content) > 0 {
		cur = cur.Content[0]
	}
	if old.Kind != cur.Kind {
		return false
	}
	restored := false
	switch cur.Kind {
	case yaml.ScalarNode:
		if strings.Contains(old.Value, "${") && expandEnvVars(old.Value) == cur.Value {
			cur.Value, cur.Style, cur.Tag = old.Value, old.Style, old.Tag
			restored = true
		}
	case yaml.MappingNode:
		oldVals := make(map[string]*yaml.Node, len(old.Content)/2)
		for i := 0; i+1 < len(old.Content); i += 2 {
			oldVals[old.Content[i].Value] = old.Content[i+1]
		}
		for i := 0; i+1 < len(cur.Content); i += 2 {
			if ov, ok := oldVals[cur.Content[i].Value]; ok && restoreEnvRefs(ov, cur.Content[i+1]) {
				restored = true
			}
		}
	case yaml.SequenceNode:
		for i := 0; i < len(cur.Content) && i < len(old.Content); i++ {
			if restoreEnvRefs(old.Content[i], cur.Content[i]) {
				restored = true
			}
		}
	}
	return restored
}
