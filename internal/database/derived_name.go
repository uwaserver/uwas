package database

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// DisambiguatedName returns name with a short hash of host appended, trimmed
// so the result stays within max bytes. Schema names derived from a host with
// a lossy mapping ("." and "-" both become "_") collide for two distinct valid
// hosts ("a.b.com" and "a-b.com"); the hash of the full host keeps the two
// apart while the plain name stays in use wherever it is not yet taken.
func DisambiguatedName(name, host string, max int) string {
	sum := sha256.Sum256([]byte(strings.ToLower(host)))
	hash := hex.EncodeToString(sum[:])[:8]
	if max <= 0 {
		return ""
	}
	if max <= len(hash) {
		return hash[:max]
	}
	suffix := "_" + hash
	if len(name)+len(suffix) > max {
		name = name[:max-len(suffix)]
	}
	return name + suffix
}
