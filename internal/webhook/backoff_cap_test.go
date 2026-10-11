package webhook

import (
	"testing"
	"time"
)

// F2470: the retry delay doubled without a ceiling (attempt 20 ≈ 6 days),
// overflowed negative at attempt 40 and to zero from 64, and retry_max was
// only rejected when negative.
func TestWebhookBackoffCapped(t *testing.T) {
	want := map[int]time.Duration{
		1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 4: 8 * time.Second,
		5: 16 * time.Second, 6: 32 * time.Second,
		7: maxWebhookBackoff, 8: maxWebhookBackoff, 20: maxWebhookBackoff,
		40: maxWebhookBackoff, 63: maxWebhookBackoff, 64: maxWebhookBackoff,
		1000: maxWebhookBackoff,
		0:    time.Second, -5: time.Second, // never zero or negative
	}
	for attempt, w := range want {
		if got := webhookBackoff(attempt); got != w {
			t.Errorf("webhookBackoff(%d) = %v, want %v", attempt, got, w)
		}
	}
	for attempt := -3; attempt < 200; attempt++ {
		if d := webhookBackoff(attempt); d <= 0 || d > maxWebhookBackoff {
			t.Fatalf("webhookBackoff(%d) = %v outside (0, %v]", attempt, d, maxWebhookBackoff)
		}
	}
}

func TestWebhookMaxRetriesClamped(t *testing.T) {
	for in, want := range map[int]int{
		0: 0, 1: 1, 3: 3, maxWebhookRetries: maxWebhookRetries,
		maxWebhookRetries + 1: maxWebhookRetries, 1000: maxWebhookRetries,
	} {
		if got := webhookMaxRetries(in); got != want {
			t.Errorf("webhookMaxRetries(%d) = %d, want %d", in, got, want)
		}
	}
}
