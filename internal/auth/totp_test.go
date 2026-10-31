package auth

import (
	"strings"
	"testing"
	"time"
)

func TestTOTPGenerationAndVerification(t *testing.T) {
	secret, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret failed: %v", err)
	}
	if len(secret) < 16 {
		t.Fatalf("Secret length too short: %s", secret)
	}

	svg, err := GenerateQRCodeSVG(secret, "admin", "Paperless-ngx")
	if err != nil {
		t.Fatalf("GenerateQRCodeSVG failed: %v", err)
	}
	if !strings.HasPrefix(svg, "<svg") || !strings.HasSuffix(svg, "</svg>") {
		t.Fatalf("Invalid SVG output: %s", svg)
	}

	code, err := CalculateTOTP(secret, time.Now())
	if err != nil {
		t.Fatalf("CalculateTOTP failed: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("Expected 6 digit code, got %s", code)
	}

	if !VerifyCode(secret, code) {
		t.Fatalf("VerifyCode failed for valid code %s", code)
	}

	if VerifyCode(secret, "000000") && code != "000000" {
		t.Fatalf("VerifyCode should have rejected invalid code")
	}

	recovery := GenerateRecoveryCodes()
	if len(recovery) != 8 {
		t.Fatalf("Expected 8 recovery codes, got %d", len(recovery))
	}
}

