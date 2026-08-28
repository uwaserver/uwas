package deploy

import (
	"os"
	"strings"
	"testing"
)

// GIT_ASKPASS was hardcoded to /bin/true, which does not exist on macOS
// (true lives in /usr/bin) or in some minimal images. git answers a missing
// GIT_ASKPASS with "cannot run ...", so the guard against a GUI credential
// prompt was itself an error on those hosts.

func envValue(env []string, key string) (string, int) {
	prefix := key + "="
	value, count := "", 0
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			value = strings.TrimPrefix(kv, prefix)
			count++
		}
	}
	return value, count
}

// Whatever is set must exist and be executable, on every platform.
func TestDefaultGitEnvAskpassExists(t *testing.T) {
	path, count := envValue(defaultGitEnv(), "GIT_ASKPASS")
	if count > 1 {
		t.Errorf("GIT_ASKPASS %d kez ayarlandı", count)
	}
	if path == "" {
		t.Skip("bu hostta no-op ikili bulunamadı; GIT_ASKPASS bilinçli olarak atlandı")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("GIT_ASKPASS=%s mevcut değil: %v — git \"cannot run\" hatası verir", path, err)
	}
	if info.IsDir() {
		t.Fatalf("GIT_ASKPASS=%s bir dizin", path)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("GIT_ASKPASS=%s çalıştırılabilir değil (mod %v)", path, info.Mode())
	}
}

func TestDefaultGitEnvBlocksPrompting(t *testing.T) {
	env := defaultGitEnv()
	if v, _ := envValue(env, "GIT_TERMINAL_PROMPT"); v != "0" {
		t.Errorf("GIT_TERMINAL_PROMPT = %q, want 0", v)
	}
	if v, _ := envValue(env, "GIT_ALLOW_PROTOCOL"); v != "https:ssh:git" {
		t.Errorf("GIT_ALLOW_PROTOCOL = %q", v)
	}
}

// With a token, the real helper must be the only GIT_ASKPASS in the slice:
// a leftover no-op entry points at a binary that answers nothing.
func TestGitAuthEnvReplacesAskpass(t *testing.T) {
	env, url, cleanup, err := gitAuthEnv("https://github.com/user/repo.git", "", "ghp_test123")
	if err != nil {
		t.Fatalf("gitAuthEnv: %v", err)
	}
	defer cleanup()

	if url != "https://github.com/user/repo.git" {
		t.Errorf("clone URL = %q", url)
	}

	path, count := envValue(env, "GIT_ASKPASS")
	if count != 1 {
		t.Errorf("GIT_ASKPASS %d kez var, 1 bekleniyordu — no-op yardımcı ortamda kaldı", count)
	}
	if path == "" {
		t.Fatal("token varken GIT_ASKPASS ayarlanmadı")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("askpass yardımcısı mevcut değil: %v", err)
	}
	if !strings.Contains(path, "uwas-git-askpass") {
		t.Errorf("GIT_ASKPASS=%s token yardımcısına işaret etmiyor", path)
	}

	cleanup()
	if _, err := os.Stat(path); err == nil {
		t.Error("cleanup askpass yardımcısını silmedi — token diskte kaldı")
	}
}

// Without a token the no-op helper stays in place.
func TestGitAuthEnvWithoutTokenKeepsNoop(t *testing.T) {
	env, _, cleanup, err := gitAuthEnv("https://github.com/user/repo.git", "", "")
	if err != nil {
		t.Fatalf("gitAuthEnv: %v", err)
	}
	defer cleanup()

	path, count := envValue(env, "GIT_ASKPASS")
	if count > 1 {
		t.Errorf("GIT_ASKPASS %d kez var", count)
	}
	if path != "" && strings.Contains(path, "uwas-git-askpass") {
		t.Error("token yokken token yardımcısı ayarlandı")
	}
}

func TestSetEnvReplacesAndAppends(t *testing.T) {
	env := []string{"A=1", "GIT_ASKPASS=/eski", "B=2"}
	env = setEnv(env, "GIT_ASKPASS", "/yeni")
	if v, count := envValue(env, "GIT_ASKPASS"); v != "/yeni" || count != 1 {
		t.Errorf("değiştirme başarısız: %q x%d", v, count)
	}
	if len(env) != 3 {
		t.Errorf("uzunluk = %d, want 3", len(env))
	}

	env = setEnv([]string{"A=1"}, "YOK", "x")
	if v, _ := envValue(env, "YOK"); v != "x" {
		t.Errorf("ekleme başarısız: %q", v)
	}
}
