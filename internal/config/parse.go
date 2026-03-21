package config

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

func parseByteSize(s string) (ByteSize, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty byte size")
	}

	// Find where the number ends and unit begins
	i := 0
	for i < len(s) && (s[i] == '.' || unicode.IsDigit(rune(s[i]))) {
		i++
	}

	numStr := s[:i]
	unit := strings.TrimSpace(s[i:])

	num, err := strconv.ParseFloat(numStr, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid byte size %q: %w", s, err)
	}

	switch strings.ToUpper(unit) {
	case "", "B":
		return ByteSize(num), nil
	case "K", "KB":
		return ByteSize(num * float64(KB)), nil
	case "M", "MB":
		return ByteSize(num * float64(MB)), nil
	case "G", "GB":
		return ByteSize(num * float64(GB)), nil
	default:
		return 0, fmt.Errorf("unknown byte unit %q in %q", unit, s)
	}
}
