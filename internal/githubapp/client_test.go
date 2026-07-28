package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// generateTestPrivateKey generates a temporary RSA private key PEM for test purposes.
func generateTestPrivateKey(t *testing.T) []byte {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test private key: %v", err)
	}

	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	}

	return pem.EncodeToMemory(block)
}

func TestGenerateAppJWT(t *testing.T) {
	pemKey := generateTestPrivateKey(t)
	appID := "123456"

	jwt, err := GenerateAppJWT(appID, pemKey)
	if err != nil {
		t.Fatalf("failed to generate JWT: %v", err)
	}

	// Verify JWT segments
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts in JWT, got %d", len(parts))
	}

	// Decode header
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("failed to decode header: %v", err)
	}
	var header map[string]string
	_ = json.Unmarshal(headerBytes, &header)

	if header["alg"] != "RS256" || header["typ"] != "JWT" {
		t.Errorf("invalid header values: %v", header)
	}

	// Decode claims
	claimsBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("failed to decode claims: %v", err)
	}
	var claims map[string]interface{}
	_ = json.Unmarshal(claimsBytes, &claims)

	if claims["iss"] != appID {
		t.Errorf("expected iss to be %s, got %v", appID, claims["iss"])
	}
}

func TestClient_GetInstallationToken_Cache(t *testing.T) {
	pemKey := generateTestPrivateKey(t)
	hitCount := 0

	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{
			"token": "ghs_testtoken12345",
			"expires_at": "2036-07-28T18:00:00Z"
		}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "12345", pemKey)
	ctx := context.Background()

	// First call -> should hit mock server
	token1, err := client.GetInstallationToken(ctx, 999)
	if err != nil {
		t.Fatal(err)
	}
	if token1 != "ghs_testtoken12345" {
		t.Errorf("unexpected token: %s", token1)
	}
	if hitCount != 1 {
		t.Errorf("expected 1 request to server, got %d", hitCount)
	}

	// Second call -> should read from cache
	token2, err := client.GetInstallationToken(ctx, 999)
	if err != nil {
		t.Fatal(err)
	}
	if token2 != "ghs_testtoken12345" {
		t.Errorf("unexpected token: %s", token2)
	}
	if hitCount != 1 {
		t.Errorf("expected request to be cached, but got second server hit: %d", hitCount)
	}
}

func TestClient_ListPRFiles_Pagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.RawQuery, "page=2") {
			_, _ = w.Write([]byte(`[
				{"filename": "file2.go", "status": "added", "patch": "@@ -0,0 +1 @@\n+println(2)"}
			]`))
			return
		}

		// First page
		w.Header().Set("Link", `<http://`+r.Host+`/repos/owner/repo/pulls/1/files?per_page=1&page=2>; rel="next"`)
		_, _ = w.Write([]byte(`[
			{"filename": "file1.go", "status": "modified", "patch": "@@ -1 +1 @@\n-println(1)\n+println(11)"}
		]`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "12345", nil)
	files, err := client.ListPRFiles(context.Background(), "fake-token", "owner", "repo", 1)
	if err != nil {
		t.Fatalf("failed to list files: %v", err)
	}

	if len(files) != 2 {
		t.Fatalf("expected 2 files gathered from pages, got %d", len(files))
	}

	if files[0].Path != "file1.go" || files[1].Path != "file2.go" {
		t.Errorf("unexpected file paths parsed: %s, %s", files[0].Path, files[1].Path)
	}
}
