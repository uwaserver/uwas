package admin

// F38001: the one-time 2FA operations burned a single, server-wide "last used
// time step". A code is only a replay when the SAME secret already used that
// step; a different secret (2FA disabled, then set up again with a new secret)
// has never seen it. With the global counter, the first valid code of the new
// secret inside the same ±skew window was refused as "invalid code" and counted
// as a failed authentication attempt toward the IP lockout.

import (
	"net/http"
	"testing"
)

func TestTOTPStepBurnIsPerSecret(t *testing.T) {
	s := testServer()

	// Enable with secret A (consumes step N), then disable with a code one
	// step ahead (consumes N+1).
	a := totpActivationSetup(t, s, "admin")
	if got := totpActivationVerify(s, "admin", totpActivationCode(t, a, 0)); got != http.StatusOK {
		t.Fatalf("enable A = %d", got)
	}
	if got := totpDisable(t, s, a, 1); got != http.StatusOK {
		t.Fatalf("disable A = %d", got)
	}

	// Set up a brand-new secret B and confirm it with its current code.
	b := totpActivationSetup(t, s, "admin")
	if b == a {
		t.Fatal("setup returned the same secret twice")
	}
	before := len(s.RateLimitMap())
	got := totpActivationVerify(s, "admin", totpActivationCode(t, b, 0))
	t.Logf("EXPECTED: 200 (secret B never used this step)\nACTUAL: %d", got)
	if got != http.StatusOK {
		t.Fatalf("PROBLEM CONFIRMED: first valid code of a new secret refused (%d)", got)
	}
	if totpActiveSecret(s) != b {
		t.Fatal("secret B was not activated")
	}
	if after := len(s.RateLimitMap()); after != before {
		t.Fatalf("a valid code was counted as an authentication failure (%d -> %d entries)", before, after)
	}
}

func TestTOTPStepReplayStillRefusedPerSecret(t *testing.T) {
	s := testServer()
	a := totpActivationSetup(t, s, "admin")
	if got := totpActivationVerify(s, "admin", totpActivationCode(t, a, 0)); got != http.StatusOK {
		t.Fatalf("enable = %d", got)
	}
	// Same secret, same step: a replay of the enabling code.
	if got := totpDisable(t, s, a, 0); got != http.StatusUnauthorized {
		t.Fatalf("replayed step disabled 2FA = %d, want 401", got)
	}
	// An earlier step of the same secret is refused too.
	if got := totpDisable(t, s, a, -1); got != http.StatusUnauthorized {
		t.Fatalf("earlier step accepted = %d, want 401", got)
	}
	// A later step is fine.
	if got := totpDisable(t, s, a, 1); got != http.StatusOK {
		t.Fatalf("later step = %d, want 200", got)
	}
}

func TestTOTPUsedStepsArePruned(t *testing.T) {
	s := testServer()
	a := totpActivationSetup(t, s, "admin")
	if got := totpActivationVerify(s, "admin", totpActivationCode(t, a, 0)); got != http.StatusOK {
		t.Fatalf("enable = %d", got)
	}
	// Records too old to ever match a code again are dropped on the next burn.
	s.totpStepMu.Lock()
	for i := 0; i < 50; i++ {
		s.usedTOTPSteps["stale-"+string(rune('a'+i%26))+string(rune('a'+i/26))] = 1
	}
	s.totpStepMu.Unlock()
	if got := totpDisable(t, s, a, 1); got != http.StatusOK {
		t.Fatalf("disable = %d", got)
	}
	s.totpStepMu.Lock()
	n := len(s.usedTOTPSteps)
	s.totpStepMu.Unlock()
	if n != 1 {
		t.Fatalf("used-step map keeps %d entries, want only the live secret's", n)
	}
}
