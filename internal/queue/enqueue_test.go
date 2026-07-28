package queue

import (
	"testing"
)

// TestEnqueuer_MatchReviewer verifies the whitelist matching logic for assignees and comment body mentions.
func TestEnqueuer_MatchReviewer(t *testing.T) {
	logins := []string{"alice", "bob", "carol"}
	e := NewEnqueuer(nil, logins)

	// 1. Match on assignee (Trigger A)
	matched, ok := e.matchReviewer([]string{"eve", "BOB"}, "")
	if !ok || matched != "bob" {
		t.Errorf("expected to match bob, got %q, %v", matched, ok)
	}

	// 2. Match on mention in comment (Trigger B)
	matched, ok = e.matchReviewer(nil, "Please check this PR @Carol")
	if !ok || matched != "carol" {
		t.Errorf("expected to match carol, got %q, %v", matched, ok)
	}

	// 3. No match
	matched, ok = e.matchReviewer([]string{"eve"}, "Hello world @frank")
	if ok || matched != "" {
		t.Errorf("expected no match, got %q, %v", matched, ok)
	}

	// 4. Priority check (alice should match first even if bob is present)
	matched, ok = e.matchReviewer([]string{"bob", "alice"}, "Hey @bob @alice")
	if !ok || matched != "alice" {
		t.Errorf("expected to match alice first due to config order, got %q, %v", matched, ok)
	}
}
