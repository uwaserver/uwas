package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// F2800: hosts (cPanel and most others) write AuthUserFile as an absolute path.
// The converter drops absolute paths (it never reads the file), which used to
// drop the whole authentication requirement with it: the protected directory
// was served to anyone. UWAS cannot verify htpasswd credentials, so every form
// of AuthUserFile must fail closed, like the relative form already did.
func TestHtaccessAbsoluteAuthUserFileFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		ht   string
		want int
	}{
		{"relative (control)", "AuthType Basic\nAuthUserFile .htpasswd\nRequire valid-user\n", http.StatusForbidden},
		{"absolute", "AuthType Basic\nAuthName \"P\"\nAuthUserFile /home/u/.htpasswds/site/passwd\nRequire valid-user\n", http.StatusForbidden},
		{"traversal", "AuthType Basic\nAuthUserFile ../../etc/passwd\nRequire valid-user\n", http.StatusForbidden},
		{"absolute without AuthType", "AuthUserFile /home/u/.htpasswd\nRequire valid-user\n", http.StatusForbidden},
		{"absolute inside active IfModule", "<IfModule mod_auth_basic.c>\nAuthType Basic\nAuthUserFile /srv/pw\nRequire valid-user\n</IfModule>\n", http.StatusForbidden},
		{"absolute inside skipped IfModule (control)", "<IfModule mod_not_loaded_anywhere.c>\nAuthUserFile /srv/pw\nRequire valid-user\n</IfModule>\n", http.StatusOK},
		{"AuthType only (control)", "AuthType Basic\nAuthName \"P\"\n", http.StatusOK},
		{"no auth (control)", "Options -Indexes\n", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := phpAuthTestServer(t, authTestRoot(t, tc.ht, map[string]string{"secret.html": "SECRET"}))
			rec := authGet(t, h, "/secret.html")
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}

	t.Run("absolute guard in a parent directory covers the subdirectory", func(t *testing.T) {
		root := authTestRoot(t, "AuthUserFile /home/u/pw\nRequire valid-user\n", nil)
		sub := filepath.Join(root, "a")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "x.html"), []byte("SECRET"), 0o644); err != nil {
			t.Fatal(err)
		}
		if rec := authGet(t, phpAuthTestServer(t, root), "/a/x.html"); rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rec.Code)
		}
	})
}
