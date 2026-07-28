package githubapp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"time"
)

// GenerateAppJWT generates a signed RS256 JWT token for GitHub App authentication.
// appID is the GitHub App ID, and privateKeyPEM is the raw bytes of the RSA PEM private key.
func GenerateAppJWT(appID string, privateKeyPEM []byte) (string, error) {
	if appID == "" {
		return "", errors.New("app ID cannot be empty")
	}

	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return "", errors.New("failed to parse private key PEM block")
	}

	// Try ParsePKCS1PrivateKey first
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Fallback to PKCS8
		pk8Key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return "", fmt.Errorf("failed to parse private key (tried PKCS1 & PKCS8): %w", err)
		}
		var ok bool
		key, ok = pk8Key.(*rsa.PrivateKey)
		if !ok {
			return "", errors.New("private key is not an RSA private key")
		}
	}

	header := map[string]string{
		"alg": "RS256",
		"typ": "JWT",
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		return "", err
	}

	now := time.Now()
	// iat is backdated by 60s to account for minor clock drifts between server and GitHub
	claims := map[string]interface{}{
		"iat": now.Unix() - 60,
		"exp": now.Unix() + 9*60, // 9 minutes lifetime
		"iss": appID,
	}
	claimsBytes, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}

	encode := func(b []byte) string {
		return base64.RawURLEncoding.EncodeToString(b)
	}

	payload := encode(headerBytes) + "." + encode(claimsBytes)

	// Sign the header.claims payload
	hasher := sha256.New()
	hasher.Write([]byte(payload))
	hashed := hasher.Sum(nil)

	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hashed)
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT: %w", err)
	}

	return payload + "." + encode(signature), nil
}
