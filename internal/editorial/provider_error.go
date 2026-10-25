package editorial

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Adi-ty/daywire/internal/content"
	"github.com/Adi-ty/daywire/internal/store"
)

// ProviderError distinguishes an API failure from an editorial rejection.
// Only allowlisted machine identifiers enter its public error string. Unknown
// messages remain in private diagnostics, never in terminal/CI output.
type ProviderError struct {
	Status                                int
	Code, Type, Parameter, DiagnosticPath string
}

func (e *ProviderError) Error() string {
	message := fmt.Sprintf("Groq HTTP %d", e.Status)
	for _, field := range []struct{ name, value string }{{"code", e.Code}, {"type", e.Type}, {"parameter", e.Parameter}} {
		if field.value != "" {
			message += "; " + field.name + "=" + field.value
		}
	}
	if e.DiagnosticPath != "" {
		message += "; private diagnostic: " + e.DiagnosticPath
	}
	return message
}

func knownIdentifier(value string) string {
	switch value {
	case "json_validate_failed", "structured_generation_failed", "invalid_request_error",
		"invalid_api_key", "authentication_error", "permission_denied", "model_not_found",
		"context_length_exceeded", "rate_limit_exceeded", "server_error",
		"response_format", "response_format.json_schema", "max_completion_tokens", "model", "messages":
		return value
	default:
		return ""
	}
}

func (g *Groq) responseError(resp *http.Response, request []byte, estimate, completion int) error {
	var envelope struct {
		Error struct {
			Message          string `json:"message"`
			Type             string `json:"type"`
			Code             string `json:"code"`
			Parameter        string `json:"param"`
			FailedGeneration string `json:"failed_generation"`
		} `json:"error"`
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	decoded := readErr == nil && len(raw) <= 64<<10 && json.Unmarshal(raw, &envelope) == nil
	err := &ProviderError{Status: resp.StatusCode}
	if decoded {
		err.Code = knownIdentifier(envelope.Error.Code)
		err.Type = knownIdentifier(envelope.Error.Type)
		err.Parameter = knownIdentifier(envelope.Error.Parameter)
	}
	if g.DiagnosticsDir == "" {
		return err
	}
	redact := func(s string) string {
		if g.APIKey != "" {
			s = strings.ReplaceAll(s, g.APIKey, "[REDACTED]")
		}
		return s
	}
	// Keep the provider's explanation and request identity, not a full prompt,
	// credentials, or failed generated text. Never salvage failed_generation as
	// accepted content. A failed request remains a failure.
	diagnostic := struct {
		At                        time.Time `json:"at"`
		Status                    int       `json:"http_status"`
		Model                     string    `json:"model"`
		RequestID                 string    `json:"request_id"`
		RequestHash               string    `json:"request_hash"`
		EstimatedInput            int       `json:"estimated_input_tokens"`
		CompletionCap             int       `json:"completion_cap"`
		Decoded                   bool      `json:"error_body_decoded"`
		Code                      string    `json:"code"`
		Type                      string    `json:"type"`
		Parameter                 string    `json:"parameter"`
		Message                   string    `json:"message"`
		FailedGenerationBytes     int       `json:"failed_generation_bytes"`
		FailedGenerationValidJSON bool      `json:"failed_generation_valid_json"`
	}{g.clock().UTC(), resp.StatusCode, redact(g.Model), redact(resp.Header.Get("X-Request-ID")), content.Hash(request), estimate, completion, decoded,
		redact(envelope.Error.Code), redact(envelope.Error.Type), redact(envelope.Error.Parameter), redact(envelope.Error.Message),
		len(envelope.Error.FailedGeneration), json.Valid([]byte(envelope.Error.FailedGeneration))}
	path := filepath.Join(g.DiagnosticsDir, "groq-"+diagnostic.RequestHash[:16]+".json")
	if saveErr := store.JSON(path, diagnostic); saveErr != nil {
		if g.Logf != nil {
			g.Logf("Could not save private Groq error diagnostic")
		}
	} else {
		err.DiagnosticPath = path
	}
	return err
}
