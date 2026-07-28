package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const sha256Prefix = "sha256="

// VerifySignature validates a GitHub webhook signature using HMAC-SHA256.
// signatureHeader is expected to be in format "sha256=<hex-signature>".
// Returns true if the signature is valid, false otherwise.
func VerifySignature(body []byte, signatureHeader string, secret string) bool {
	if secret == "" {
		return false
	}

	if !strings.HasPrefix(signatureHeader, sha256Prefix) {
		return false
	}

	// Extract hex signature
	hexSig := signatureHeader[len(sha256Prefix):]
	expectedSig, err := hex.DecodeString(hexSig)
	if err != nil {
		return false
	}

	// Compute HMAC-SHA256 of the body
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	actualSig := mac.Sum(nil)

	// Constant-time compare to prevent timing attacks
	return hmac.Equal(actualSig, expectedSig)
}
