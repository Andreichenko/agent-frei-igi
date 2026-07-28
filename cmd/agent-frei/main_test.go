package main

import (
	"testing"
)

// TestVersionConstant checks that the Version constant is defined and not empty.
func TestVersionConstant(t *testing.T) {
	if Version == "" {
		t.Error("expected Version constant to be defined, got empty string")
	}
}
