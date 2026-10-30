package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A file over the per-file extraction cap used to be copied up to the cap and
// accepted: the site arrived with a silently truncated file and no error. The
// file must be refused and reported, not delivered cut short.
func TestCPanelImportRefusesFileOverPerFileLimit(t *testing.T) {
	old := maxExtractFileBytes
	maxExtractFileBytes = 1024
	t.Cleanup(func() { maxExtractFileBytes = old })

	arc := filepath.Join(t.TempDir(), "backup.tar.gz")
	root := "cpmove-alice"
	writeCPanelBackup(t, arc, map[string]string{
		root + "/cp/alice":                     "BWLIMIT=0\nDNS=example.com\n",
		root + "/userdata/main":                "---\ncp_user: alice\nmain_domain: example.com\n",
		root + "/userdata/example.com":         "documentroot: /home/alice/public_html\nservername: example.com\n",
		root + "/homedir/public_html/ok.txt":   "small file",
		root + "/homedir/public_html/big.bin":  strings.Repeat("B", 4096),
		root + "/homedir/public_html/edge.bin": strings.Repeat("E", 1024),
	})
	dst := t.TempDir()
	res, err := ImportCPanelBackup(arc, dst, false)
	if err != nil {
		t.Fatal(err)
	}

	sizes := map[string]int64{}
	_ = filepath.Walk(dst, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			sizes[filepath.Base(p)] = fi.Size()
		}
		return nil
	})
	t.Logf("EXPECTED: big.bin refused and reported, ok.txt and edge.bin intact\nACTUAL: files=%v errors=%v", sizes, res.Errors)

	if sz, ok := sizes["big.bin"]; ok {
		t.Errorf("PROBLEM CONFIRMED: big.bin was delivered truncated to %d bytes (archive entry is 4096)", sz)
	}
	reported := false
	for _, e := range res.Errors {
		if strings.Contains(e, "big.bin") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("the refused file is not reported in Errors: %v", res.Errors)
	}
	// Controls: a small file, and a file of exactly the limit, import whole.
	if sizes["ok.txt"] != int64(len("small file")) {
		t.Errorf("ok.txt = %d bytes, want %d", sizes["ok.txt"], len("small file"))
	}
	if sizes["edge.bin"] != 1024 {
		t.Errorf("a file of exactly the limit = %d bytes, want 1024", sizes["edge.bin"])
	}
}
