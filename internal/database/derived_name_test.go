package database

import (
	"strings"
	"testing"
)

func TestDisambiguatedName(t *testing.T) {
	a := DisambiguatedName("a_b_com_db", "a.b.com", 60)
	b := DisambiguatedName("a_b_com_db", "a-b.com", 60)
	if a == b {
		t.Fatalf("distinct hosts share %q", a)
	}
	if !strings.HasPrefix(a, "a_b_com_db_") || len(a) != len("a_b_com_db_")+8 {
		t.Errorf("unexpected form %q", a)
	}
	if a != DisambiguatedName("a_b_com_db", "A.B.COM", 60) {
		t.Error("not stable across host case")
	}
	long := DisambiguatedName(strings.Repeat("x", 60), "h.example", 60)
	if len(long) != 60 {
		t.Errorf("len = %d, want 60", len(long))
	}
	if !ValidDBIdentifier(long) {
		t.Errorf("%q is not a valid identifier", long)
	}
}

func TestDisambiguatedNameRespectsTinyLimits(t *testing.T) {
	for max := 0; max <= 9; max++ {
		got := DisambiguatedName("database", "tenant.example", max)
		if len(got) > max {
			t.Fatalf("max %d: len(%q) = %d", max, got, len(got))
		}
	}
	if got := DisambiguatedName("database", "tenant.example", 8); got == "" {
		t.Fatal("positive hash-sized limit lost all disambiguation")
	}
}
