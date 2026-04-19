package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
)

func TestVerifyGitHubSignatureSuccess(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	secret := "test-secret"

	headers := make(http.Header)
	headers.Set("X-Hub-Signature-256", "sha256="+sign(secret, body))

	if err := Verify("github", secret, body, headers); err != nil {
		t.Fatalf("expected signature to pass, got error: %v", err)
	}
}

func TestVerifyGitHubSignatureFail(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	secret := "test-secret"

	headers := make(http.Header)
	headers.Set("X-Hub-Signature-256", "sha256=deadbeef")

	if err := Verify("github", secret, body, headers); err == nil {
		t.Fatal("expected signature verification to fail")
	}
}

func TestExtractEventType(t *testing.T) {
	headers := make(http.Header)
	headers.Set("X-GitHub-Event", "Push")

	event := ExtractEventType("github", headers)
	if event != "push" {
		t.Fatalf("expected push, got %s", event)
	}
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
