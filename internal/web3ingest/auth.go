package web3ingest

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

const (
	headerIngestKey       = "X-MMO-Ingest-Key"
	headerIngestSignature = "X-MMO-Ingest-Signature"
	headerAuthorization   = "Authorization"
	// MaxIngestBodyBytes максимальный размер тела POST /v1/indexer/ingest.
	MaxIngestBodyBytes    = 1 << 20 // 1 MiB
	prefixSignatureSHA256 = "sha256="
)

// ComputeHMACSignatureHex returns lowercase hex(HMAC-SHA256(secret, body)).
func ComputeHMACSignatureHex(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// CheckIngestAuth validates optional API key and/or HMAC for POST /v1/indexer/ingest.
// If apiKey is non-empty, require matching X-MMO-Ingest-Key or Authorization: Bearer <key>.
// If hmacSecret is non-empty, require X-MMO-Ingest-Signature as hex(hmac-sha256(body)) or sha256=<hex>.
// If both are non-empty, both checks must pass. If both are empty, no auth (open endpoint).
func CheckIngestAuth(h http.Header, body []byte, apiKey, hmacSecret string) error {
	apiKey = strings.TrimSpace(apiKey)
	hmacSecret = strings.TrimSpace(hmacSecret)
	if apiKey == "" && hmacSecret == "" {
		return nil
	}
	if apiKey != "" {
		got := strings.TrimSpace(h.Get(headerIngestKey))
		if got == "" {
			got = bearerToken(h.Get(headerAuthorization))
		}
		if len(got) != len(apiKey) || subtle.ConstantTimeCompare([]byte(got), []byte(apiKey)) != 1 {
			return fmt.Errorf("invalid or missing ingest API key")
		}
	}
	if hmacSecret != "" {
		sigRaw := strings.TrimSpace(h.Get(headerIngestSignature))
		if sigRaw == "" {
			return fmt.Errorf("missing %s", headerIngestSignature)
		}
		wantHex := ComputeHMACSignatureHex(hmacSecret, body)
		gotHex := strings.TrimPrefix(strings.ToLower(sigRaw), prefixSignatureSHA256)
		gotBytes, err := hex.DecodeString(gotHex)
		if err != nil {
			return fmt.Errorf("invalid signature encoding")
		}
		wantBytes, err := hex.DecodeString(wantHex)
		if err != nil {
			return fmt.Errorf("internal signature error")
		}
		if len(gotBytes) != len(wantBytes) || subtle.ConstantTimeCompare(gotBytes, wantBytes) != 1 {
			return fmt.Errorf("invalid HMAC signature")
		}
	}
	return nil
}

func bearerToken(auth string) string {
	auth = strings.TrimSpace(auth)
	const p = "Bearer "
	if len(auth) <= len(p) || !strings.EqualFold(auth[:len(p)], p) {
		return ""
	}
	return strings.TrimSpace(auth[len(p):])
}
