package firewall

import (
	"fmt"
	"testing"
	"time"
)

// EnableWithRollback documents that a `within` of 0 enables with no rollback.
// A rollback scheduled by an earlier, still-unconfirmed call must not survive
// it: the stale timer would later `ufw disable` a firewall the caller just
// asked to keep on. (r148 F23900)
func TestEnableWithRollbackZeroCancelsPendingRollback(t *testing.T) {
	rs := &recordingStub{}
	rs.install(t)

	if err := EnableWithRollback(time.Hour, nil); err != nil {
		t.Fatal(err)
	}
	// Control: a positive window leaves a rollback pending.
	if pending, _ := rollbackStatus(); !pending {
		t.Fatal("control failed: EnableWithRollback(1h) left no pending rollback")
	}

	if err := EnableWithRollback(0, nil); err != nil {
		t.Fatal(err)
	}
	pending, _ := rollbackStatus()
	fmt.Printf("EXPECTED: pending=false after EnableWithRollback(0)\nACTUAL: pending=%v\n", pending)
	if pending {
		fmt.Println("PROBLEM CONFIRMED")
		t.Fatal("stale rollback timer survived EnableWithRollback(0)")
	}
	fmt.Println("PROBLEM NOT REPRODUCED")
}
