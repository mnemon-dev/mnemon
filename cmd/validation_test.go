package cmd

import (
	"strings"
	"testing"
)

func TestRequirePositiveLimit(t *testing.T) {
	if err := requirePositiveLimit("--limit", 1); err != nil {
		t.Fatalf("valid limit returned error: %v", err)
	}
	err := requirePositiveLimit("--limit", 0)
	if err == nil {
		t.Fatal("expected invalid limit error")
	}
	if !strings.Contains(err.Error(), "--limit must be at least 1") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRequireNonNegativeFloat(t *testing.T) {
	if err := requireNonNegativeFloat("--threshold", 0); err != nil {
		t.Fatalf("valid threshold returned error: %v", err)
	}
	err := requireNonNegativeFloat("--threshold", -0.1)
	if err == nil {
		t.Fatal("expected invalid threshold error")
	}
	if !strings.Contains(err.Error(), "--threshold must be non-negative") {
		t.Fatalf("unexpected error: %v", err)
	}
}
