package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var ErrInvalidSignature = errors.New("invalid signature")

func Verify(provider, secret string, body []byte, headers http.Header) error {
	if strings.TrimSpace(secret) == "" {
		return errors.New("secret is empty")
	}

	normalizedProvider := normalizeProvider(provider)
	expectedHex := calcHMACSHA256Hex(secret, body)

	switch normalizedProvider {
	case "github":
		raw := strings.TrimSpace(headers.Get("X-Hub-Signature-256"))
		if raw == "" {
			return ErrInvalidSignature
		}
		if err := verifySHA256PrefixedSignature(raw, expectedHex); err != nil {
			return err
		}
		return nil
	case "gitea":
		raw := strings.TrimSpace(headers.Get("X-Hub-Signature-256"))
		if raw != "" {
			return verifySHA256PrefixedSignature(raw, expectedHex)
		}
		raw = strings.TrimSpace(headers.Get("X-Gitea-Signature"))
		if raw == "" {
			return ErrInvalidSignature
		}
		if secureCompareHex(raw, expectedHex) {
			return nil
		}
		return ErrInvalidSignature
	default:
		raw := strings.TrimSpace(headers.Get("X-Webhook-Signature-256"))
		if raw == "" {
			return ErrInvalidSignature
		}
		return verifySHA256PrefixedSignature(raw, expectedHex)
	}
}

func ExtractEventType(provider string, headers http.Header) string {
	switch normalizeProvider(provider) {
	case "github":
		return normalizeEvent(headers.Get("X-GitHub-Event"))
	case "gitea":
		event := normalizeEvent(headers.Get("X-Gitea-Event"))
		if event != "" {
			return event
		}
		return normalizeEvent(headers.Get("X-GitHub-Event"))
	default:
		event := normalizeEvent(headers.Get("X-Webhook-Event"))
		if event != "" {
			return event
		}
		event = normalizeEvent(headers.Get("X-GitHub-Event"))
		if event != "" {
			return event
		}
		return normalizeEvent(headers.Get("X-Gitea-Event"))
	}
}

func verifySHA256PrefixedSignature(rawSignature, expectedHex string) error {
	if !strings.HasPrefix(rawSignature, "sha256=") {
		return fmt.Errorf("%w: signature header must start with sha256=", ErrInvalidSignature)
	}
	actualHex := strings.TrimSpace(strings.TrimPrefix(rawSignature, "sha256="))
	if !secureCompareHex(actualHex, expectedHex) {
		return ErrInvalidSignature
	}
	return nil
}

func secureCompareHex(actualHex, expectedHex string) bool {
	actualHex = strings.ToLower(strings.TrimSpace(actualHex))
	expectedHex = strings.ToLower(strings.TrimSpace(expectedHex))
	if len(actualHex) == 0 || len(expectedHex) == 0 {
		return false
	}
	actualBytes, err := hex.DecodeString(actualHex)
	if err != nil {
		return false
	}
	expectedBytes, err := hex.DecodeString(expectedHex)
	if err != nil {
		return false
	}
	if len(actualBytes) != len(expectedBytes) {
		return false
	}
	return subtle.ConstantTimeCompare(actualBytes, expectedBytes) == 1
}

func calcHMACSHA256Hex(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func normalizeProvider(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return "github"
	}
	return provider
}

func normalizeEvent(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
