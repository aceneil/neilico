package service

import (
	"testing"
	"time"
)

func TestIsHeartbeatExpired(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	timeout := 60 * time.Second
	tests := []struct {
		name     string
		lastSeen *time.Time
		want     bool
	}{
		{name: "nil is expired", lastSeen: nil, want: true},
		{name: "within timeout", lastSeen: timePtr(now.Add(-59 * time.Second)), want: false},
		{name: "exact timeout", lastSeen: timePtr(now.Add(-60 * time.Second)), want: false},
		{name: "past timeout", lastSeen: timePtr(now.Add(-61 * time.Second)), want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IsHeartbeatExpired(test.lastSeen, timeout, now); got != test.want {
				t.Fatalf("IsHeartbeatExpired() = %v, want %v", got, test.want)
			}
		})
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}
