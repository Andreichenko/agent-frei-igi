package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// computeHMAC calculates the HMAC-SHA256 hex string for a given payload and secret.
func computeHMAC(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	secret := "super-secret-webhook-key"
	payload := []byte(`{"zen": "Non-blocking is better than blocking."}`)

	// 1. Success case: Valid signature
	validSig := computeHMAC(payload, secret)
	if !VerifySignature(payload, validSig, secret) {
		t.Error("expected VerifySignature to return true for valid signature, got false")
	}

	// 2. Error case: Wrong signature
	invalidSig := "sha256=0000000000000000000000000000000000000000000000000000000000000000"
	if VerifySignature(payload, invalidSig, secret) {
		t.Error("expected VerifySignature to return false for incorrect signature, got true")
	}

	// 3. Error case: Missing sha256= prefix
	noPrefixSig := "0000000000000000000000000000000000000000000000000000000000000000"
	if VerifySignature(payload, noPrefixSig, secret) {
		t.Error("expected VerifySignature to return false for signature missing prefix, got true")
	}

	// 4. Error case: Empty secret configuration
	if VerifySignature(payload, validSig, "") {
		t.Error("expected VerifySignature to return false when secret is empty, got true")
	}

	// 5. Error case: Invalid hex format in signature
	badHexSig := "sha256=invalid-hex-characters-!!!"
	if VerifySignature(payload, badHexSig, secret) {
		t.Error("expected VerifySignature to return false for invalid hex signature, got true")
	}
}
