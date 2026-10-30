//go:build unix

package middleware

// F45303: the converter temp file is created with an unpredictable name (F1570),
// but it lives in the tenant's directory and the tenant can list that directory.
// Between CreateTemp and the converter's open(2) it swaps the temp file for a
// symlink to any host file: the converter, running as the server user (root),
// then writes its output through the link, and the later rename publishes the
// link itself as the optimized variant. The swap is gated deterministically
// through the convertExecFn seam, which runs after the temp file exists and
// before the converter starts.

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func swapTempForSymlink(t *testing.T, dir, victim string, swapped *bool) func() {
	t.Helper()
	ol, oe := convertLookPathFn, convertExecFn
	convertLookPathFn = func(string) (string, error) { return "/bin/sh", nil }
	convertExecFn = func(name string, args ...string) *exec.Cmd {
		out := args[len(args)-1]
		// What the tenant does after listing its directory.
		temps, _ := filepath.Glob(filepath.Join(dir, ".uwas-img-*.tmp"))
		for _, tmp := range temps {
			if os.Remove(tmp) == nil && os.Symlink(victim, tmp) == nil {
				*swapped = true
			}
		}
		return exec.Command("/bin/sh", "-c", "printf 'CONVERTED' > \"$0\"", out)
	}
	return func() { convertLookPathFn, convertExecFn = ol, oe }
}

func TestConvertImageTempSwapNeverWritesOrPublishesTheLink(t *testing.T) {
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("ORIGINAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "p.png")
	if err := os.WriteFile(src, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	var swapped bool
	defer swapTempForSymlink(t, dir, victim, &swapped)()

	convertImage(src, src+".webp", "webp")

	if !swapped {
		t.Fatal("test setup: the temp file was never swapped")
	}
	got, _ := os.ReadFile(victim)
	t.Logf("EXPECTED: victim ORIGINAL, no symlink published\nACTUAL: victim=%q", got)
	if string(got) != "ORIGINAL" {
		t.Errorf("PROBLEM CONFIRMED: the converter wrote through the swapped temp file into the victim: %q", got)
	}
	if fi, err := os.Lstat(src + ".webp"); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("PROBLEM CONFIRMED: the swapped symlink was published as the variant %s", src+".webp")
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".uwas-img-*")); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// Control: no swap — the variant is published as a regular file.
func TestConvertImageWithoutSwapStillPublishes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "p.png")
	if err := os.WriteFile(src, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls int32
	defer stubImageConverter(&calls)()
	if !convertImage(src, src+".webp", "webp") {
		t.Fatal("control: conversion failed")
	}
	fi, err := os.Lstat(src + ".webp")
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o644 {
		t.Fatalf("control: variant = %v %v, want a regular 0644 file", fi, err)
	}
	if b, _ := os.ReadFile(src + ".webp"); string(b) != "CONVERTED" {
		t.Errorf("control: variant = %q", b)
	}
}

// The serve side opens the variant after path checks a swap can outrun: a
// symlink or FIFO at the variant name must not be served.
func TestOpenVariantNoFollow(t *testing.T) {
	dir := t.TempDir()
	reg := filepath.Join(dir, "ok.webp")
	if err := os.WriteFile(reg, []byte("webp"), 0o644); err != nil {
		t.Fatal(err)
	}
	if f, info, err := openVariantNoFollow(reg); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("control: regular variant refused: %v", err)
	} else {
		f.Close()
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.webp")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if f, _, err := openVariantNoFollow(link); err == nil {
		f.Close()
		t.Error("a symlink variant was opened")
	}
}
