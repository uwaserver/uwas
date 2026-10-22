package doctor

import (
	"fmt"
	"io/fs"
	"os"
	"testing"
)

// fileStat reports every listed path as a regular file and the rest as absent.
func fileStat(files ...string) func(string) (os.FileInfo, error) {
	set := map[string]bool{}
	for _, f := range files {
		set[f] = true
	}
	return func(name string) (os.FileInfo, error) {
		if set[name] {
			return fakeFileInfo{name: name, isDir: false}, nil
		}
		return nil, &os.PathError{Op: "stat", Path: name, Err: os.ErrNotExist}
	}
}

// r148 F23901: a web root that is a regular file is not a usable web root.
func TestCheckWebRoot_RegularFileIsNotOK(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fileStat("/srv/notadir")
	c := checkWebRoot("/srv/notadir", false)
	fmt.Printf("EXPECTED: fail\nACTUAL: %s %q\n", c.Status, c.Message)
	if c.Status == StatusOK {
		fmt.Println("PROBLEM CONFIRMED")
		t.Fatalf("regular file accepted as web root: %q", c.Message)
	}
	// Control: a real directory is fine.
	osStatFn = fakeStat(map[string]bool{"/srv/web": true})
	if c := checkWebRoot("/srv/web", false); c.Status != StatusOK {
		t.Fatalf("control: directory rejected: %s %q", c.Status, c.Message)
	}
}

// r148 F23901: a config path that is a directory is not a config file.
func TestCheckConfigFile_DirectoryIsNotOK(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = fakeStat(map[string]bool{"/etc/uwas": true}) // fakeStat: directories
	c := checkConfigFile("/etc/uwas")
	fmt.Printf("EXPECTED: fail\nACTUAL: %s %q\n", c.Status, c.Message)
	if c.Status == StatusOK {
		fmt.Println("PROBLEM CONFIRMED")
		t.Fatalf("directory accepted as config file: %q", c.Message)
	}
	// Control: a regular file is fine.
	osStatFn = fileStat("/etc/uwas/uwas.yaml")
	if c := checkConfigFile("/etc/uwas/uwas.yaml"); c.Status != StatusOK {
		t.Fatalf("control: file rejected: %s %q", c.Status, c.Message)
	}
}

// Auto-discovery must skip a directory named like a default config path.
func TestCheckConfigFile_AutoDiscoverySkipsDirectory(t *testing.T) {
	saveAndRestoreHooks(t)
	osStatFn = func(name string) (os.FileInfo, error) {
		switch name {
		case "/etc/uwas/uwas.yaml":
			return fakeFileInfo{name: name, isDir: true}, nil
		case "/opt/uwas/uwas.yaml":
			return fakeFileInfo{name: name, isDir: false}, nil
		}
		return nil, &os.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	c := checkConfigFile("")
	if c.Status != StatusOK || c.Message != "/opt/uwas/uwas.yaml" {
		t.Fatalf("got %s %q, want the file at /opt/uwas/uwas.yaml", c.Status, c.Message)
	}
}
