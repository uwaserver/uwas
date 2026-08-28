package fastcgi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/router"
)

// php.max_upload was the one field in PHPConfig that nothing read: defaulted
// in defaults.go, merged, documented in SPECIFICATION.md, never applied. A
// domain configured for 64MB uploads got whatever the system php.ini said,
// which is 2M by PHP's own default.

func uploadEnv(t *testing.T, docRoot string, maxUpload config.ByteSize) map[string]string {
	t.Helper()

	ctx := &router.RequestContext{
		Request:      httptest.NewRequest(http.MethodPost, "/upload.php", nil),
		DocumentRoot: docRoot,
		VHostName:    "php.test",
	}
	return BuildEnv(ctx, "/srv/www/upload.php", "/upload.php", "", nil, maxUpload)
}

func adminDirectives(t *testing.T, env map[string]string) map[string]string {
	t.Helper()

	out := map[string]string{}
	for _, line := range strings.Split(env["PHP_ADMIN_VALUE"], "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

func TestMaxUploadReachesPHP(t *testing.T) {
	env := uploadEnv(t, "/srv/www", 64*config.MB)
	d := adminDirectives(t, env)

	if got, want := d["upload_max_filesize"], "67108864"; got != want {
		t.Errorf("upload_max_filesize = %q, want %q — php.max_upload PHP'ye ulaşmıyor", got, want)
	}
	// post_max_size must exceed upload_max_filesize: PHP measures the whole
	// request body against it, and multipart overhead rides along with the
	// file. Equal values would reject an upload of exactly the configured size.
	if got, want := d["post_max_size"], "68157440"; got != want {
		t.Errorf("post_max_size = %q, want %q", got, want)
	}

	// The isolation directive must survive alongside it.
	if !strings.Contains(d["open_basedir"], "/srv/www") {
		t.Errorf("open_basedir kayboldu: %q", d["open_basedir"])
	}
}

// An unset max_upload must add nothing, leaving the system php.ini in charge.
func TestMaxUploadUnsetAddsNothing(t *testing.T) {
	d := adminDirectives(t, uploadEnv(t, "/srv/www", 0))

	if _, ok := d["upload_max_filesize"]; ok {
		t.Error("max_upload ayarsızken direktif yazıldı — sistem php.ini'si eziliyor")
	}
	if _, ok := d["post_max_size"]; ok {
		t.Error("max_upload ayarsızken post_max_size yazıldı")
	}
	if !strings.Contains(d["open_basedir"], "/srv/www") {
		t.Error("open_basedir kayboldu")
	}
}

// Without a document root the open_basedir block does not run; the upload
// limit must still be applied.
func TestMaxUploadWithoutDocumentRoot(t *testing.T) {
	d := adminDirectives(t, uploadEnv(t, "", 8*config.MB))

	if got, want := d["upload_max_filesize"], "8388608"; got != want {
		t.Errorf("upload_max_filesize = %q, want %q", got, want)
	}
	if _, ok := d["open_basedir"]; ok {
		t.Error("DocumentRoot yokken open_basedir yazıldı")
	}
}

func TestMaxUploadNegativeIgnored(t *testing.T) {
	if d := adminDirectives(t, uploadEnv(t, "/srv/www", -1)); d["upload_max_filesize"] != "" {
		t.Errorf("negatif max_upload uygulandı: %q", d["upload_max_filesize"])
	}
}

// A client cannot raise its own limit: PHP_ADMIN_VALUE is stripped from
// request headers, and PHP_ADMIN_VALUE outranks PHP_VALUE and ini_set.
func TestMaxUploadNotOverridableByClientHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/upload.php", nil)
	req.Header.Set("Php-Admin-Value", "upload_max_filesize = 999999999")
	ctx := &router.RequestContext{Request: req, DocumentRoot: "/srv/www", VHostName: "php.test"}

	env := BuildEnv(ctx, "/srv/www/upload.php", "/upload.php", "", nil, 8*config.MB)
	if got := adminDirectives(t, env)["upload_max_filesize"]; got != "8388608" {
		t.Errorf("istemci başlığı sınırı ezdi: %q", got)
	}
}
