package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F62003: a document-root copy that failed half way (disk full) left the
// partial tree in the new site's root and the domain was still handed back for
// registration, so a half-imported site went live and a retry was refused
// ("already exists and is not empty").
func TestCPanelImportFailedCopyLeavesNoPartialSite(t *testing.T) {
	orig := copyDirFn
	defer func() { copyDirFn = orig }()
	copyDirFn = func(src, dst string) error {
		if err := os.WriteFile(filepath.Join(dst, "wp-config.php"), []byte("SECRET"), 0o644); err != nil {
			return err
		}
		return errors.New("write: no space left on device")
	}

	dst := t.TempDir()
	res, err := ImportCPanelBackup(cpanelDocRootArchive(t), dst, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range res.Domains {
		if d.Domain == "main.test" {
			t.Errorf("main.test is still imported after its copy failed: %+v", res.Domains)
		}
	}
	if entries, err := os.ReadDir(filepath.Join(dst, "main.test", "public_html")); err == nil && len(entries) > 0 {
		t.Errorf("partial site left behind: %d entries", len(entries))
	}
	reported := false
	for _, e := range res.Errors {
		if strings.Contains(e, "copy main.test") {
			reported = true
		}
	}
	if !reported {
		t.Errorf("copy failure not reported: %v", res.Errors)
	}
}

// Control: with a working copy the same archive imports the site.
func TestCPanelImportSuccessfulCopyStillImports(t *testing.T) {
	dst := t.TempDir()
	res, err := ImportCPanelBackup(cpanelDocRootArchive(t), dst, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range res.Domains {
		found = found || d.Domain == "main.test"
	}
	if !found || readImported(dst, "main.test", "index.html") != "MAIN" {
		t.Errorf("control: main.test not imported: domains=%v errors=%v", res.Domains, res.Errors)
	}
}
