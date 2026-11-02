package cache

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCacheKeyEncodedDuplicateQueryOrder(t *testing.T) {
	key := func(q string) string {
		return GenerateKey(httptest.NewRequest("GET", "http://site.test/?"+q, nil), nil)
	}
	for _, tc := range [][2]string{
		{"a=1&%61=2", "%61=2&a=1"}, {"a+b=1&a%20b=2", "a%20b=2&a+b=1"},
		{"ids[]=1&ids%5B%5D=2", "ids%5B%5D=2&ids[]=1"},
		{"a=1&%61=2" + strings.Repeat("&z=0", 20), "%61=2&a=1" + strings.Repeat("&z=0", 20)},
	} {
		if key(tc[0]) == key(tc[1]) {
			t.Error("encoded duplicate ordering collapsed", tc)
		}
	}
	for _, tc := range [][2]string{{"b=2&a=1", "a=1&b=2"}, {"%61=1&z=2", "z=2&%61=1"}, {"", ""}} {
		if key(tc[0]) != key(tc[1]) {
			t.Error("unaffected permutation", tc)
		}
	}
	if key("%zz=1&a=1") == key("%zz=2&a=1") {
		t.Fatal("invalid-escape values collapsed")
	}
}
