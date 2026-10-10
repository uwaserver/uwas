package domainutil

import (
	"reflect"
	"testing"
)

// F1481: RemoveDomainAlias must not rewrite its input's backing array; the
// admin handler passes the live config slice, which snapshots share.
func TestRemoveDomainAliasDoesNotMutateInput(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		host    string
		want    []string
		wantNil bool
	}{
		{"middle", []string{"a.example", "b.example", "c.example"}, "b.example", []string{"a.example", "c.example"}, false},
		{"first, case-insensitive", []string{"A.example", "b.example"}, "a.EXAMPLE", []string{"b.example"}, false},
		{"duplicates", []string{"a.example", "b.example", "a.example"}, "a.example", []string{"b.example"}, false},
		{"all removed", []string{"a.example"}, "a.example", []string{}, false},
		{"no match", []string{"a.example", "b.example"}, "z.example", []string{"a.example", "b.example"}, false},
		{"nil input", nil, "a.example", nil, true},
		{"empty host", []string{"a.example"}, "", []string{"a.example"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			orig := append([]string(nil), c.in...)
			snapshot := c.in // shares the backing array
			got := RemoveDomainAlias(c.in, c.host)
			if !reflect.DeepEqual(snapshot, orig) {
				t.Errorf("input mutated: %v, want %v", snapshot, orig)
			}
			if c.wantNil {
				if got != nil {
					t.Errorf("got %v, want nil", got)
				}
				return
			}
			if len(got) != len(c.want) || (len(got) > 0 && !reflect.DeepEqual(got, c.want)) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
