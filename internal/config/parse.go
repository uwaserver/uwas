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

	var result float64
	switch strings.ToUpper(unit) {
	case "", "B":
		result = num
	case "K", "KB":
		result = num * float64(KB)
	case "M", "MB":
		result = num * float64(MB)
	case "G", "GB":
		result = num * float64(GB)
	default:
		return 0, fmt.Errorf("unknown byte unit %q in %q", unit, s)
	}
	if result < 0 || result >= 1<<63 {
		return 0, fmt.Errorf("byte size %q overflows int64", s)
	}
	return ByteSize(result), nil
}
