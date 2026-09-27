package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// These limits apply to ordinary control-plane JSON requests. Webhooks, policy
// documents and runbooks have their own independently enforced size limits.
const maxControlJSONBytes int64 = 1 << 20
const maxClientRequestIDBytes = 128

// boundedRequestID accepts only compact ASCII identifiers suitable for logs,
// response headers and persisted audit metadata. Untrusted invalid IDs are
// replaced with a fresh UUID by middleware, never reflected or persisted.
func boundedRequestID(header string) string {
	value := strings.TrimSpace(header)
	if value == "" || len(value) > maxClientRequestIDBytes {
		return ""
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			c >= '0' && c <= '9' || c == '-' || c == '_' ||
			c == '.' || c == ':' {
			continue
		}
		return ""
	}
	return value
}

func decodeBoundedJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.ContentLength > maxControlJSONBytes {
		writeError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "JSON request body exceeds 1 MiB", nil)
		return false
	}
	reader := http.MaxBytesReader(w, r.Body, maxControlJSONBytes)
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		var limitErr *http.MaxBytesError
		if errors.As(err, &limitErr) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "JSON request body exceeds 1 MiB", nil)
		} else {
			writeError(w, r, http.StatusBadRequest, "invalid_json", "request body could not be read", nil)
		}
		return false
	}
	body = bytes.TrimSpace(body)
	// A null document decodes into a zero-valued struct without an error.
	// It is not a valid request to any control-plane mutation.
	if len(body) == 0 || bytes.Equal(body, []byte("null")) {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "request body must be a JSON object", nil)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
		return false
	}
	// Without a second decode, concatenated JSON documents can be silently
	// accepted even though trailing bytes may have a different interpretation.
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "request body must contain exactly one JSON document", nil)
		return false
	}
	return true
}
