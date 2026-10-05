package apps

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreLoadCanonicalExtension(t *testing.T) {
	for _, exts := range [][]string{{".yaml"}, {".yml"}, {".yaml", ".yml"}} {
		st := NewStore(t.TempDir())
		st.DataRoot = t.TempDir()
		for _, ext := range exts {
			cmd := "./primary"
			if ext == ".yml" {
				cmd = "./alternate"
			}
			if err := os.WriteFile(filepath.Join(st.Dir, "worker"+ext), []byte("name: worker\nruntime: custom\ncommand: "+cmd+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 2; i++ {
			apps, skipped, err := st.Load()
			got, getErr := st.Get("worker")
			if err != nil || getErr != nil || len(skipped) != 0 || len(apps) != 1 || got == nil || apps[0].Command != got.Command || len(st.Names()) != 1 {
				t.Fatalf("extensions=%v apps=%v get=%v err=%v/%v skipped=%v", exts, apps, got, err, getErr, skipped)
			}
		}
		if err := st.Save(&App{Name: "alpha", Runtime: RuntimeCustom, Command: "./other"}); err != nil {
			t.Fatal(err)
		}
		apps, skipped, err := st.Load()
		if err != nil || len(skipped) != 0 || len(apps) != 2 || apps[0].Name != "alpha" || apps[1].Name != "worker" {
			t.Fatal("distinct names/sort")
		}
	}
	fmt.Println("FIX VERIFIED")
}
