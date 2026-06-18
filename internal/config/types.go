package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Duration wraps time.Duration for YAML/JSON unmarshaling of strings like
// "30s", "5m" or plain numbers (interpreted as seconds).
type Duration struct {
	time.Duration
}

func (d Duration) MarshalJSON() ([]byte, error) {
	if d.Duration == 0 {
		return []byte("0"), nil
	}
	return []byte(`"` + d.Duration.String() + `"`), nil
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	// Try number (seconds)
	s := strings.TrimSpace(string(b))
	if s == "null" || s == "" {
		return nil
	}
	if s[0] >= '0' && s[0] <= '9' || s[0] == '-' {
		var secs float64
		if err := json.Unmarshal(b, &secs); err != nil {
			return err
		}
		d.Duration = time.Duration(secs * float64(time.Second))
		return nil
	}
	// Try string like "30s"
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	dur, err := time.ParseDuration(str)
	if err != nil {
		return err
	}
	d.Duration = dur
	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	if d.Duration == 0 {
		return "0s", nil
	}
	return d.Duration.String(), nil
}

func (d *Duration) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		var secs int
		if err2 := unmarshal(&secs); err2 != nil {
			// Neither a duration string nor an integer; report the int-path
			// error since that was the last (and most specific) attempt.
			return err2
		}
		d.Duration = time.Duration(secs) * time.Second
		return nil
	}
	dur, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = dur
	return nil
}

// ByteSize represents a size in bytes, parsed from strings like "512MB", "10GB".
type ByteSize int64

const (
	KB ByteSize = 1024
	MB ByteSize = 1024 * KB
	GB ByteSize = 1024 * MB
)

func (b ByteSize) MarshalJSON() ([]byte, error) {
	return json.Marshal(int64(b))
}

func (b *ByteSize) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "null" {
		return nil
	}
	// Try number
	var n int64
	if err := json.Unmarshal(data, &n); err == nil {
		*b = ByteSize(n)
		return nil
	}
	// Try string like "512MB"
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	size, err := parseByteSize(str)
	if err != nil {
		return err
	}
	*b = size
	return nil
}

func (b ByteSize) MarshalYAML() (any, error) {
	if b == 0 {
		return 0, nil
	}
	if b%GB == 0 {
		return fmt.Sprintf("%dGB", b/GB), nil
	}
	if b%MB == 0 {
		return fmt.Sprintf("%dMB", b/MB), nil
	}
	if b%KB == 0 {
		return fmt.Sprintf("%dKB", b/KB), nil
	}
	return int64(b), nil
}

func (b *ByteSize) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err != nil {
		var n int64
		if err2 := unmarshal(&n); err2 != nil {
			return err
		}
		*b = ByteSize(n)
		return nil
	}
	size, err := parseByteSize(s)
	if err != nil {
		return err
	}
	*b = size
	return nil
}
