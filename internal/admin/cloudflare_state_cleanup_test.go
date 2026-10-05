package admin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCloudflareStateSaveCleansTemporaryFiles(t *testing.T) {
	cloudflareMu.Lock()
	old := cloudflareConfig
	cloudflareConfig = &cloudflareState{AccountID: "fixture-account", Tunnels: []cloudflareTunnel{}}
	cloudflareMu.Unlock()
	defer func() { cloudflareMu.Lock(); cloudflareConfig = old; cloudflareMu.Unlock() }()
	for _, fail := range []bool{false, true} {
		dir := t.TempDir()
		s := &Server{configPath: filepath.Join(dir, "uwas.yaml")}
		path := s.cloudflareStateFile()
		if fail {
			if e := os.Mkdir(path, 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(filepath.Join(path, "original"), []byte("unchanged"), 0600); e != nil {
				t.Fatal(e)
			}
		}
		entered := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			cloudflareMu.Lock()
			defer cloudflareMu.Unlock()
			close(entered)
			<-release
			done <- s.saveCloudflareStateLocked()
		}()
		<-entered
		close(release)
		e := <-done
		if (e != nil) != fail {
			t.Fatalf("fail=%t err=%v", fail, e)
		}
		entries, e := os.ReadDir(dir)
		if e != nil || len(entries) != 1 || entries[0].Name() != "cloudflare.json" {
			t.Fatalf("temporary file retained: %v err=%v", entries, e)
		}
		if fail {
			data, e := os.ReadFile(filepath.Join(path, "original"))
			if e != nil || string(data) != "unchanged" {
				t.Fatal("original directory content changed")
			}
		} else {
			data, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			var st cloudflareState
			if e := json.Unmarshal(data, &st); e != nil || st.AccountID != "fixture-account" || st.SchemaVersion != cloudflareStateSchemaCurrent {
				t.Fatal("state content or schema changed")
			}
			if stinfo, e := os.Stat(path); e != nil || stinfo.Mode().Perm() != 0600 {
				t.Fatal("file mode")
			}
			cloudflareMu.Lock()
			cloudflareConfig = nil
			e = s.saveCloudflareStateLocked()
			cloudflareMu.Unlock()
			if e != nil {
				t.Fatal(e)
			}
			if _, e := os.Stat(path); !os.IsNotExist(e) {
				t.Fatal("nil config did not remove final file")
			}
			cloudflareMu.Lock()
			cloudflareConfig = &cloudflareState{AccountID: "fixture-account", Tunnels: []cloudflareTunnel{}}
			cloudflareMu.Unlock()
		}
	}
	cloudflareMu.Lock()
	e := (&Server{}).saveCloudflareStateLocked()
	cloudflareMu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	fmt.Println("FIX VERIFIED")
}
