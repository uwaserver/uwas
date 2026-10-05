package phpmanager

import "testing"

func TestInstallationsOwnExtensionSnapshots(t *testing.T) {
	for _, ext := range [][]string{nil, {}, {"json", "curl"}} {
		m := New(testLogger())
		m.installations = []PHPInstall{{Version: "8.3.1", Extensions: ext}}
		first := m.Installations()
		second := m.Installations()
		ready := make(chan struct{})
		release := make(chan struct{})
		done := make(chan struct{})
		go func() {
			close(ready)
			<-release
			if len(first[0].Extensions) > 0 {
				first[0].Extensions[0] = "caller"
			}
			first[0].Version = "caller"
			close(done)
		}()
		<-ready
		fresh := m.Installations()
		close(release)
		<-done
		if second[0].Version != "8.3.1" || fresh[0].Version != "8.3.1" || m.Installations()[0].Version != "8.3.1" {
			t.Fatal("scalar alias")
		}
		for _, snapshot := range [][]PHPInstall{second, fresh, m.Installations()} {
			if (snapshot[0].Extensions == nil) != (ext == nil) {
				t.Fatal("nil semantics")
			}
			if len(ext) > 0 && snapshot[0].Extensions[0] != "json" {
				t.Fatal("nested alias", snapshot)
			}
		}
	}
	m := New(testLogger())
	if len(m.Installations()) != 0 {
		t.Fatal("empty manager")
	}
	t.Log("FIX VERIFIED")
}
