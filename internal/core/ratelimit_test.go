package core

import (
	"testing"
	"time"
)

func TestUserRateLimiter(t *testing.T) {
	limiter := NewUserRateLimiter(3, 100*time.Millisecond)

	userID := int64(12345)

	// 1st request -> allowed
	if !limiter.Allow(userID) {
		t.Errorf("expected request 1 to be allowed")
	}

	// 2nd request -> allowed
	if !limiter.Allow(userID) {
		t.Errorf("expected request 2 to be allowed")
	}

	// 3rd request -> allowed
	if !limiter.Allow(userID) {
		t.Errorf("expected request 3 to be allowed")
	}

	// 4th request -> blocked (rate limited)
	if limiter.Allow(userID) {
		t.Errorf("expected request 4 to be blocked by rate limit")
	}

	// Different user should still be allowed
	otherUser := int64(67890)
	if !limiter.Allow(otherUser) {
		t.Errorf("expected other user request to be allowed")
	}

	// Wait for window to expire
	time.Sleep(120 * time.Millisecond)

	// Should be allowed again
	if !limiter.Allow(userID) {
		t.Errorf("expected request after window reset to be allowed")
	}
}
