package core

import (
	"sync"
	"time"
)

// UserRateLimiter limits the number of actions a Telegram user can perform within a time window.
type UserRateLimiter struct {
	mu           sync.Mutex
	limits       map[int64][]time.Time
	maxRequests  int
	windowPeriod time.Duration
}

// NewUserRateLimiter creates a rate limiter with maxRequests per window.
func NewUserRateLimiter(maxRequests int, windowPeriod time.Duration) *UserRateLimiter {
	limiter := &UserRateLimiter{
		limits:       make(map[int64][]time.Time),
		maxRequests:  maxRequests,
		windowPeriod: windowPeriod,
	}

	// Periodic cleanup of stale entries
	go limiter.cleanupLoop(10 * time.Minute)

	return limiter
}

// Allow checks if the given user is allowed to perform an action. Returns true if allowed, false if rate limited.
func (l *UserRateLimiter) Allow(userID int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-l.windowPeriod)

	timestamps, exists := l.limits[userID]
	if !exists {
		l.limits[userID] = []time.Time{now}
		return true
	}

	// Filter out expired timestamps
	valid := make([]time.Time, 0, len(timestamps))
	for _, t := range timestamps {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= l.maxRequests {
		l.limits[userID] = valid
		return false
	}

	valid = append(valid, now)
	l.limits[userID] = valid
	return true
}

func (l *UserRateLimiter) cleanupLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	for range ticker.C {
		l.mu.Lock()
		now := time.Now()
		cutoff := now.Add(-l.windowPeriod)
		for id, timestamps := range l.limits {
			var valid []time.Time
			for _, t := range timestamps {
				if t.After(cutoff) {
					valid = append(valid, t)
				}
			}
			if len(valid) == 0 {
				delete(l.limits, id)
			} else {
				l.limits[id] = valid
			}
		}
		l.mu.Unlock()
	}
}
