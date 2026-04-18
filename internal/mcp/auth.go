package mcp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

// AuthenticateHTTPRequest applies the same auth model as /ingest:
// plain token or sha256 HMAC over the raw request body.
func AuthenticateHTTPRequest(w http.ResponseWriter, r *http.Request, expectedToken string, logger *zap.Logger) error {
	if expectedToken == "" {
		return nil
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	provided := r.Header.Get(ingestAuthHeader)
	if provided == "" {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return fmt.Errorf("missing auth header")
	}
	if strings.HasPrefix(provided, "sha256:") {
		sig := provided[7:]
		providedSig, err := hex.DecodeString(sig)
		if err != nil || len(providedSig) != sha256.Size {
			logger.Warn("invalid HMAC signature format in request", zap.String("remote_addr", r.RemoteAddr))
			http.Error(w, "invalid authentication signature", http.StatusUnauthorized)
			return fmt.Errorf("invalid signature format")
		}
		bodyBytes, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024*1024))
		if err != nil {
			if _, ok := err.(*http.MaxBytesError); ok {
				http.Error(w, "payload too large (max 1MB)", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "failed to read request body", http.StatusBadRequest)
			}
			return err
		}
		mac := hmac.New(sha256.New, []byte(expectedToken))
		_, _ = mac.Write(bodyBytes)
		if !hmac.Equal(providedSig, mac.Sum(nil)) {
			logger.Warn("invalid HMAC signature in request", zap.String("remote_addr", r.RemoteAddr))
			http.Error(w, "invalid authentication signature", http.StatusUnauthorized)
			return fmt.Errorf("invalid signature")
		}
		r.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
		return nil
	}
	if !hmac.Equal([]byte(provided), []byte(expectedToken)) {
		logger.Warn("invalid auth token in request", zap.String("remote_addr", r.RemoteAddr))
		http.Error(w, "invalid authentication token", http.StatusUnauthorized)
		return fmt.Errorf("invalid token")
	}
	return nil
}
