package phpmanager

import "testing"

func TestGetExtensionsOwnsSnapshot(t *testing.T) {
	for _, input := range [][]string{nil, {}, {"curl", "json"}} {
		m := New(testLogger())
		m.installations = []PHPInstall{{Version: "8.4.19", Extensions: input}}
		first, err := m.GetExtensions("8.4")
		if err != nil {
			t.Fatal(err)
		}
		second, err := m.GetExtensions("8.4.19")
		if err != nil {
			t.Fatal(err)
		}
		if (first == nil) != (input == nil) || len(first) != len(input) {
			t.Fatal("nil/empty semantics")
		}
		ready, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		go func() {
			close(ready)
			<-release
			if len(first) > 0 {
				first[0] = "caller"
			}
			close(done)
		}()
		<-ready
		snapshot := m.Installations()
		close(release)
		<-done
		fresh, err := m.GetExtensions("8.4")
		if err != nil {
			t.Fatal(err)
		}
		if len(input) > 0 {
			for _, s := range [][]string{second, snapshot[0].Extensions, fresh} {
				if s[0] != "curl" {
					t.Fatal("aliased slice", s)
				}
			}
		}
	}
	m := New(testLogger())
	if _, err := m.GetExtensions("missing"); err == nil {
		t.Fatal("missing version accepted")
	}
}
