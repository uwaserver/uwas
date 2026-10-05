package config

import (
	"gopkg.in/yaml.v3"
	"testing"
	"time"
)

func TestDurationYAMLActualDecoderSeconds(t *testing.T) {
	for _, v := range []struct {
		s string
		d time.Duration
	}{{"30", 30 * time.Second}, {"0", 0}, {"-2", -2 * time.Second}, {"1h", time.Hour}, {"9223372036", 9223372036 * time.Second}} {
		var d Duration
		if e := yaml.Unmarshal([]byte(v.s), &d); e != nil || d.Duration != v.d {
			t.Fatal(v, d, e)
		}
	}
	for _, s := range []string{"9223372037", "-9223372037", "wrong"} {
		d := Duration{time.Minute}
		if e := yaml.Unmarshal([]byte(s), &d); e == nil || d.Duration != time.Minute {
			t.Fatal(s, d, e)
		}
	}
	t.Log("FIX VERIFIED")
}
