package middleware

import "testing"

// F49000: §16.2 blocks vendor/, but only the Composer one — a front-end
// theme's /vendor/bootstrap/ and /assets/vendor/ must keep working.
func TestIsComposerVendorPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/vendor/autoload.php", true},
		{"/vendor/autoload_runtime.php", true},
		{"/vendor/composer/installed.json", true},
		{"/vendor/phpunit/phpunit/src/Util/PHP/eval-stdin.php", true},
		{"/vendor/bin/phpunit", true},
		{"/app/vendor/composer/LICENSE", true},
		{"/wp-content/plugins/x/vendor/autoload.php", true},
		{"/Vendor/Composer/installed.json", true},
		{"/vendor/bootstrap/css/bootstrap.css", false},
		{"/vendor/jquery/jquery.min.js", false},
		{"/assets/vendor/aos/aos.js", false},
		{"/vendor", false},
		{"/vendor/", false},
		{"/vendors/composer/x", false},
		{"/products/vendor-list.html", false},
		{"/", false},
	}
	for _, c := range cases {
		if got := isComposerVendorPath(c.path); got != c.want {
			t.Errorf("isComposerVendorPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
