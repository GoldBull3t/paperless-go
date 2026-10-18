package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
)

// GenerateSecret generates a 20-byte (160-bit) cryptographically secure Base32 TOTP secret
func GenerateSecret() (string, error) {
	bytes := make([]byte, 20)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(bytes), nil
}

// GenerateQRCodeSVG generates an inline SVG QR code representing the TOTP configuration URI
func GenerateQRCodeSVG(secret, username, issuer string) (string, error) {
	if issuer == "" {
		issuer = "Paperless-ngx"
	}
	if username == "" {
		username = "admin"
	}

	uri := fmt.Sprintf(
		"otpauth://totp/%s:%s?secret=%s&issuer=%s&algorithm=SHA1&digits=6&period=30",
		url.PathEscape(issuer),
		url.PathEscape(username),
		secret,
		url.QueryEscape(issuer),
	)

	qr, err := qrcode.New(uri, qrcode.Medium)
	if err != nil {
		return "", err
	}

	bitmap := qr.Bitmap()
	size := len(bitmap)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, size, size))
	sb.WriteString(`<rect width="100%" height="100%" fill="#ffffff"/>`)
	for y, row := range bitmap {
		for x, val := range row {
			if val {
				sb.WriteString(fmt.Sprintf(`<rect x="%d" y="%d" width="1" height="1" fill="#000000"/>`, x, y))
			}
		}
	}
	sb.WriteString(`</svg>`)

	return sb.String(), nil
}

// CalculateTOTP calculates the 6-digit TOTP code for a secret at a specific time
func CalculateTOTP(secret string, t time.Time) (string, error) {
	cleanSecret := strings.ToUpper(strings.TrimSpace(secret))
	// Add padding if missing
	if pad := len(cleanSecret) % 8; pad != 0 {
		cleanSecret += strings.Repeat("=", 8-pad)
	}

	key, err := base32.StdEncoding.DecodeString(cleanSecret)
	if err != nil {
		return "", fmt.Errorf("invalid base32 secret: %w", err)
	}

	counter := uint64(t.Unix() / 30)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	h := mac.Sum(nil)

	offset := h[len(h)-1] & 0x0f
	code := binary.BigEndian.Uint32(h[offset:offset+4]) & 0x7fffffff
	code %= 1000000

	return fmt.Sprintf("%06d", code), nil
}

// VerifyCode verifies a TOTP code against a secret within a ±1 time step tolerance (90s window)
func VerifyCode(secret, code string) bool {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return false
	}

	now := time.Now()
	// Check current, previous, and next time windows to account for slight clock drift
	windows := []time.Time{
		now,
		now.Add(-30 * time.Second),
		now.Add(30 * time.Second),
	}

	for _, w := range windows {
		expected, err := CalculateTOTP(secret, w)
		if err == nil && expected == code {
			return true
		}
	}

	return false
}

// GenerateRecoveryCodes produces 8 16-character backup recovery codes
func GenerateRecoveryCodes() []string {
	var codes []string
	for i := 0; i < 8; i++ {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		code := fmt.Sprintf("%x-%x", b[:4], b[4:])
		codes = append(codes, code)
	}
	return codes
}

