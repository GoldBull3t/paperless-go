package auth

import (
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	limiter := NewRateLimiter(3, 100*time.Millisecond, 200*time.Millisecond)

	key := "test-user-ip"

	// Initial check: allowed
	if ok, _ := limiter.Allow(key); !ok {
		t.Fatalf("Expected first attempt to be allowed")
	}

	// 2 failures
	limiter.RecordFailure(key)
	limiter.RecordFailure(key)

	if ok, _ := limiter.Allow(key); !ok {
		t.Fatalf("Expected 2 failures to still be within limit of 3")
	}

	// 3rd failure: reaches limit
	limiter.RecordFailure(key)

	if ok, remaining := limiter.Allow(key); ok {
		t.Fatalf("Expected key to be blocked after 3 failures")
	} else if remaining <= 0 {
		t.Fatalf("Expected positive remaining block time, got %v", remaining)
	}

	// Reset
	limiter.Reset(key)
	if ok, _ := limiter.Allow(key); !ok {
		t.Fatalf("Expected key to be allowed immediately after reset")
	}
}

func TestVerifyAndConsumeRecoveryCode(t *testing.T) {
	codesJSON := `["a1b2-c3d4", "e5f6-7890"]`

	// Test invalid code
	consumed, remaining := VerifyAndConsumeRecoveryCode(codesJSON, "invalid-code")
	if consumed {
		t.Fatalf("Expected invalid code not to be consumed")
	}

	// Test valid code (case insensitive)
	consumed, remaining = VerifyAndConsumeRecoveryCode(codesJSON, "A1B2-C3D4")
	if !consumed {
		t.Fatalf("Expected valid code to be consumed")
	}

	// Make sure the consumed code is no longer in remaining
	consumed2, _ := VerifyAndConsumeRecoveryCode(remaining, "a1b2-c3d4")
	if consumed2 {
		t.Fatalf("Expected used code cannot be consumed twice")
	}

	// Make sure other code is still valid
	consumed3, finalJSON := VerifyAndConsumeRecoveryCode(remaining, "e5f6-7890")
	if !consumed3 {
		t.Fatalf("Expected second code to still be valid")
	}

	if finalJSON != "[]" {
		t.Fatalf("Expected empty array remaining, got: %s", finalJSON)
	}
}

