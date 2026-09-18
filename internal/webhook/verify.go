package webhook

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
)

// verifySignature checks the tool-specific webhook signature over the raw body.
//
//	github:     X-Hub-Signature-256: sha256=<hmac-sha256 hex>
//	pagerduty:  X-PagerDuty-Signature: v1=<hmac-sha256 hex>[,v1=...]
//	jira:       X-SignalYard-Signature: sha256=<hmac-sha256 hex> (Jira has no native signing)
//	marble-jar: X-SignalYard-Signature: sha256=<hmac-sha256 hex>
func verifySignature(tool string, r *http.Request, body []byte, secret string) bool {
	if secret == "" {
		return false // fail closed: unconfigured tool secret rejects traffic
	}
	switch tool {
	case "github":
		return checkHMACHeader(r.Header.Get("X-Hub-Signature-256"), "sha256=", hmacSHA256(secret, body)) ||
			checkHMACHeader(r.Header.Get("X-Hub-Signature"), "sha1=", hmacSHA1(secret, body))
	case "pagerduty":
		want := hmacSHA256(secret, body)
		for _, part := range strings.Split(r.Header.Get("X-PagerDuty-Signature"), ",") {
			if got, ok := strings.CutPrefix(strings.TrimSpace(part), "v1="); ok && hmac.Equal([]byte(got), []byte(want)) {
				return true
			}
		}
		return false
	default: // jira, marble-jar
		return checkHMACHeader(r.Header.Get("X-SignalYard-Signature"), "sha256=", hmacSHA256(secret, body))
	}
}

func checkHMACHeader(header, prefix, want string) bool {
	got, ok := strings.CutPrefix(header, prefix)
	return ok && hmac.Equal([]byte(got), []byte(want))
}

func hmacSHA256(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func hmacSHA1(secret string, body []byte) string {
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}
