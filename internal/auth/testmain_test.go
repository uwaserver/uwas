package auth

import (
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestMain sets a low bcrypt cost for the test suite so password hashing
// doesn't dominate test runtime — especially under -race where bcrypt cost
// 14 would add several minutes per package. The production BcryptCost (14)
// is used in the compiled binary; tests only ever exercise the hashing
// logic, not the production timing characteristics.
func TestMain(m *testing.M) {
	atomic.StoreInt64(&testBcryptCost, int64(bcrypt.MinCost))
	m.Run()
}
