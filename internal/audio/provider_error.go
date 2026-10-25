package audio

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Provider messages can echo submitted text. Classify known failures into our
// own explanations rather than copying arbitrary response text into CI logs.
func speechError(resp *http.Response) error {
	var body struct {
		Detail struct {
			Status  string `json:"status"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"detail"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (16<<10)+1))
	base := fmt.Sprintf("ElevenLabs returned HTTP %d", resp.StatusCode)
	if err != nil || len(raw) > 16<<10 || json.Unmarshal(raw, &body) != nil {
		return fmt.Errorf("%s (unrecognized error response)", base)
	}
	code := body.Detail.Status
	if code == "" {
		code = body.Detail.Code
	}
	if code == "" {
		code = body.Error.Code
	}
	message := strings.ToLower(body.Detail.Message + " " + body.Error.Message)
	if (code == "payment_required" || code == "paid_plan_required") && strings.Contains(message, "free users cannot use library voices") {
		return fmt.Errorf("%s: the selected Voice Library voice requires a paid plan for API use, even with free credits; choose an API-eligible voice and update ELEVENLABS_VOICE_ID", base)
	}
	var explanation string
	switch code {
	case "insufficient_credits", "quota_exceeded":
		explanation = "insufficient credits or quota; check the remaining account credits and any API-key credit limit"
	case "payment_required", "paid_plan_required", "subscription_required":
		explanation = "payment or a subscription is required; check both the remaining credits and the selected voice's API eligibility"
	case "voice_access_denied", "voice_not_found":
		explanation = "the configured voice is unavailable to this API key; check ELEVENLABS_VOICE_ID and voice access"
	case "model_access_denied":
		explanation = "the configured speech model is unavailable to this account"
	case "detected_unusual_activity":
		explanation = "ElevenLabs has disabled free-tier access following an unusual-activity check; review the account notice or contact support"
	case "invalid_api_key", "insufficient_permissions":
		explanation = "check ELEVENLABS_API_KEY and its Text to Speech permission"
	default:
		explanation = "unclassified provider error; check account credits and voice/model eligibility"
	}
	return fmt.Errorf("%s: %s", base, explanation)
}
