//go:build !unix

package backup

import "os"

// Ownership is a unix concept; tenantOwner never applies elsewhere.
func fileOwner(info os.FileInfo) (uid, gid int, ok bool) { return 0, 0, false }
