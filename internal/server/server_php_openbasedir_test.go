package server

import (
	"errors"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/phpmanager"
)

// autoAssignPHP called AssignDomain, which leaves the web root empty.
// buildDomainINI gates upload_tmp_dir, session.save_path and sys_temp_dir on
// a non-empty web root, so a PHP domain started at boot kept the shared
// system temp directory for sessions and uploads. (open_basedir survives:
// fastcgi/env.go writes it per request.) The same domain re-assigned through
// the admin panel went through AssignDomainWithRoot and got the isolation —
// it depended on which code path happened to start the process.

type sahtePHPYonetici struct {
	atananKok  map[string]string
	ayarlar    map[string]map[string]string
	baslatilan []string
	// sıra: SetDomainConfig çağrılarının StartDomain'den önce gelmesi şart,
	// çünkü ini süreç başlarken yazılıyor.
	baslatildiktanSonraAyar bool
	atamaHatasi             error
	ayarHatasi              error
}

func yeniSahte() *sahtePHPYonetici {
	return &sahtePHPYonetici{
		atananKok: map[string]string{},
		ayarlar:   map[string]map[string]string{},
	}
}

func (f *sahtePHPYonetici) AssignDomainWithRoot(domain, version, webRoot string) (*phpmanager.DomainPHP, error) {
	if f.atamaHatasi != nil {
		return nil, f.atamaHatasi
	}
	f.atananKok[domain] = webRoot
	return &phpmanager.DomainPHP{Domain: domain, Version: version, ListenAddr: "127.0.0.1:9001"}, nil
}

func (f *sahtePHPYonetici) SetDomainConfig(domain, key, value string) error {
	for _, d := range f.baslatilan {
		if d == domain {
			f.baslatildiktanSonraAyar = true
		}
	}
	if f.ayarHatasi != nil {
		return f.ayarHatasi
	}
	if f.ayarlar[domain] == nil {
		f.ayarlar[domain] = map[string]string{}
	}
	f.ayarlar[domain][key] = value
	return nil
}

func (f *sahtePHPYonetici) StartDomain(domain string) error {
	f.baslatilan = append(f.baslatilan, domain)
	return nil
}

func testLog() *logger.Logger { return logger.New("error", "text") }

// The web root must reach the PHP manager, or open_basedir is never written.
func TestAssignPHPForDomainPassesWebRoot(t *testing.T) {
	f := yeniSahte()
	d := config.Domain{Host: "php.test", Type: "php", Root: "/srv/www/php.test"}

	if _, err := assignPHPForDomain(f, d, "8.4", testLog()); err != nil {
		t.Fatalf("assignPHPForDomain: %v", err)
	}

	if got := f.atananKok["php.test"]; got != "/srv/www/php.test" {
		t.Errorf("web root = %q, want %q — boş kök oturum/yükleme izolasyonunu kapatır", got, "/srv/www/php.test")
	}
	if len(f.baslatilan) != 1 || f.baslatilan[0] != "php.test" {
		t.Errorf("StartDomain çağrıları = %v", f.baslatilan)
	}
}

// The domain's php.ini overrides were dropped on this path entirely.
func TestAssignPHPForDomainAppliesConfigOverrides(t *testing.T) {
	f := yeniSahte()
	d := config.Domain{
		Host: "php.test",
		Type: "php",
		Root: "/srv/www/php.test",
		PHP: config.PHPConfig{
			ConfigOverrides: map[string]string{
				"memory_limit":        "256M",
				"upload_max_filesize": "32M",
			},
		},
	}

	if _, err := assignPHPForDomain(f, d, "8.4", testLog()); err != nil {
		t.Fatalf("assignPHPForDomain: %v", err)
	}

	got := f.ayarlar["php.test"]
	if got["memory_limit"] != "256M" || got["upload_max_filesize"] != "32M" {
		t.Errorf("uygulanan override'lar = %v", got)
	}
	// buildDomainINI ini'yi süreç başlarken yazıyor; sonradan set edilen bir
	// override o sürece hiç ulaşmaz.
	if f.baslatildiktanSonraAyar {
		t.Error("override StartDomain'den sonra set edildi — çalışan sürece ulaşmaz")
	}
}

// A rejected override must not take the rest, or the start, down with it.
func TestAssignPHPForDomainSurvivesRejectedOverride(t *testing.T) {
	f := yeniSahte()
	f.ayarHatasi = errors.New("disallowed key")
	d := config.Domain{
		Host: "php.test",
		Type: "php",
		Root: "/srv/www/php.test",
		PHP:  config.PHPConfig{ConfigOverrides: map[string]string{"open_basedir": "/"}},
	}

	if _, err := assignPHPForDomain(f, d, "8.4", testLog()); err != nil {
		t.Fatalf("reddedilen override atamayı düşürdü: %v", err)
	}
	if len(f.baslatilan) != 1 {
		t.Error("reddedilen override StartDomain'i engelledi")
	}
}

// An already-assigned domain must not be started a second time.
func TestAssignPHPForDomainStopsOnAssignError(t *testing.T) {
	f := yeniSahte()
	f.atamaHatasi = errors.New("domain already has a PHP assignment")

	if _, err := assignPHPForDomain(f, config.Domain{Host: "php.test"}, "8.4", testLog()); err == nil {
		t.Fatal("atama hatası yutuldu")
	}
	if len(f.baslatilan) != 0 {
		t.Errorf("atama başarısızken StartDomain çağrıldı: %v", f.baslatilan)
	}
}
