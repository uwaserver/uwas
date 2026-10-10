package filemanager

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// within runs fn and reports whether it returned inside d.
func withinDeadline(d time.Duration, fn func() error) (err error, done bool) {
	ch := make(chan error, 1)
	go func() { ch <- fn() }()
	select {
	case err = <-ch:
		return err, true
	case <-time.After(d):
		return nil, false
	}
}

func TestFIFOInWebRootDoesNotBlock(t *testing.T) {
	base := t.TempDir()
	fifo := filepath.Join(base, "pipe")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skip(err)
	}
	if err := os.WriteFile(filepath.Join(base, "ok.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := 0
	// control
	got, err := ReadFile(base, "ok.txt")
	if err != nil || string(got) != "hi" {
		t.Fatalf("control failed: %q %v", got, err)
	}
	if err := WriteFile(base, "w.txt", []byte("x")); err != nil {
		t.Fatalf("control write: %v", err)
	}
	if _, err := SaveUpload(base, "u.txt", strings.NewReader("x")); err != nil {
		t.Fatalf("control upload: %v", err)
	}

	check := func(name string, fn func() error) {
		err, done := withinDeadline(1500*time.Millisecond, fn)
		fmt.Printf("EXPECTED: %s returns promptly with error\nACTUAL:   done=%v err=%v\n", name, done, err)
		if !done || err == nil {
			bad++
		}
	}
	check("ReadFile(fifo)", func() error { _, e := ReadFile(base, "pipe"); return e })
	check("WriteFile(fifo)", func() error { return WriteFile(base, "pipe", []byte("x")) })
	check("SaveUpload(fifo)", func() error { _, e := SaveUpload(base, "pipe", bytes.NewReader([]byte("x"))); return e })

	// edge: directory still errors, big file cap still applies
	if _, err := ReadFile(base, "."); err == nil {
		t.Error("dir read should error")
		bad++
	}
	if bad > 0 {
		fmt.Println("PROBLEM CONFIRMED")
		t.Fatal("problem")
	}
	fmt.Println("PROBLEM NOT REPRODUCED")
	fmt.Println("FIX VERIFIED")
}
