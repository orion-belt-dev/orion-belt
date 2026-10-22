package chatops

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// slackSignatureMaxSkew is the replay-protection window Slack recommends:
// reject any request whose timestamp is more than 5 minutes away from now.
const slackSignatureMaxSkew = 5 * time.Minute

// slackMessage builds the Slack chat.postMessage payload for an access
// request: a section block with the human-readable text, plus an actions
// block with Approve/Deny buttons carrying the request ID as their value.
func slackMessage(channel, text, requestID string) map[string]interface{} {
	return map[string]interface{}{
		"channel": channel,
		"text":    text,
		"blocks": []map[string]interface{}{
			{
				"type": "section",
				"text": map[string]interface{}{
					"type": "mrkdwn",
					"text": text,
				},
			},
			{
				"type": "actions",
				"elements": []map[string]interface{}{
					{
						"type":      "button",
						"text":      map[string]interface{}{"type": "plain_text", "text": "Approve"},
						"style":     "primary",
						"action_id": "approve",
						"value":     requestID,
					},
					{
						"type":      "button",
						"text":      map[string]interface{}{"type": "plain_text", "text": "Deny"},
						"style":     "danger",
						"action_id": "deny",
						"value":     requestID,
					},
				},
			},
		},
	}
}

// postSlack sends a message to Slack via chat.postMessage. Slack returns
// HTTP 200 even for API-level errors, encoding failure as {"ok":false,...},
// so that field must be checked explicitly.
func (p *ChatOpsPlugin) postSlack(ctx context.Context, text, requestID string) error {
	payload := slackMessage(p.cfg.Slack.Channel, text, requestID)
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal slack payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/chat.postMessage", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build slack request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.Slack.BotToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read slack response: %w", err)
	}

	var slackResp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &slackResp); err != nil {
		return fmt.Errorf("decode slack response: %w", err)
	}
	if !slackResp.OK {
		return fmt.Errorf("slack API error: %s", slackResp.Error)
	}
	return nil
}

// verifySlackSignature implements Slack's request-signing scheme:
// HMAC-SHA256(signing_secret, "v0:"+timestamp+":"+rawBody), hex-encoded and
// prefixed with "v0=", compared in constant time. Also enforces the 5-minute
// replay window Slack recommends.
func verifySlackSignature(signingSecret, signatureHeader, timestampHeader string, rawBody []byte) error {
	if signatureHeader == "" || timestampHeader == "" {
		return fmt.Errorf("missing signature headers")
	}

	ts, err := strconv.ParseInt(timestampHeader, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp")
	}
	now := time.Now().Unix()
	skew := now - ts
	if skew < 0 {
		skew = -skew
	}
	if skew > int64(slackSignatureMaxSkew.Seconds()) {
		return fmt.Errorf("timestamp outside allowed skew")
	}

	const prefix = "v0="
	if !strings.HasPrefix(signatureHeader, prefix) {
		return fmt.Errorf("malformed signature")
	}
	gotSig, err := hex.DecodeString(strings.TrimPrefix(signatureHeader, prefix))
	if err != nil {
		return fmt.Errorf("malformed signature encoding")
	}

	base := "v0:" + timestampHeader + ":" + string(rawBody)
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte(base))
	expectedSig := mac.Sum(nil)

	if !hmac.Equal(gotSig, expectedSig) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}

// slackInteractionPayload is the JSON shape Slack sends URL-encoded (in the
// "payload" form field) for a block-kit button click.
type slackInteractionPayload struct {
	Actions []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	} `json:"actions"`
	ResponseURL string `json:"response_url"`
	User        struct {
		Username string `json:"username"`
		Name     string `json:"name"`
	} `json:"user"`
}

// handleSlackInteraction verifies the Slack request signature, resolves the
// access request (approve/deny), and best-effort replaces the original
// message via response_url. Slack requires a bare 200 within 3 seconds; since
// the resolve call is local (Orion Belt's own API) this is done synchronously.
func (p *ChatOpsPlugin) handleSlackInteraction(w http.ResponseWriter, r *http.Request) {
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}

	if err := verifySlackSignature(
		p.cfg.Slack.SigningSecret,
		r.Header.Get("X-Slack-Signature"),
		r.Header.Get("X-Slack-Request-Timestamp"),
		rawBody,
	); err != nil {
		log.Printf("[ChatOpsPlugin] slack signature verification failed: %v", err)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	form, err := url.ParseQuery(string(rawBody))
	if err != nil {
		http.Error(w, "invalid form body", http.StatusBadRequest)
		return
	}
	rawPayload := form.Get("payload")
	if rawPayload == "" {
		http.Error(w, "missing payload", http.StatusBadRequest)
		return
	}

	var payload slackInteractionPayload
	if err := json.Unmarshal([]byte(rawPayload), &payload); err != nil {
		http.Error(w, "invalid payload JSON", http.StatusBadRequest)
		return
	}
	if len(payload.Actions) == 0 {
		http.Error(w, "no actions in payload", http.StatusBadRequest)
		return
	}

	action := payload.Actions[0]
	var actionName string
	switch action.ActionID {
	case "approve":
		actionName = "approve"
	case "deny":
		actionName = "deny"
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}

	resolveErr := p.api.resolveAction(r.Context(), action.Value, actionName)

	var resultText string
	if resolveErr != nil {
		resultText = fmt.Sprintf("Failed to %s request `%s`: %v", actionName, action.Value, resolveErr)
		log.Printf("[ChatOpsPlugin] slack %s failed for request %s: %v", actionName, action.Value, resolveErr)
	} else {
		actor := payload.User.Username
		if actor == "" {
			actor = payload.User.Name
		}
		verb := "Approved"
		if actionName == "deny" {
			verb = "Denied"
		}
		resultText = fmt.Sprintf("%s by %s", verb, actor)
	}

	// Best-effort: replace the original message so the channel reflects the
	// outcome. Failure here must not affect the HTTP response to Slack.
	if payload.ResponseURL != "" {
		go p.postSlackResponseURL(payload.ResponseURL, resultText)
	}

	w.WriteHeader(http.StatusOK)
}

// slackResponseOrigin is where Slack's response_url always points. Requests
// are already signature-checked; posting only to this origin also keeps a
// leaked signing secret from turning the gateway into a request proxy. A
// variable so tests can point it at a local server.
var slackResponseOrigin = "https://hooks.slack.com"

func (p *ChatOpsPlugin) postSlackResponseURL(responseURL, text string) {
	u, err := url.Parse(responseURL)
	if err != nil || u.Scheme+"://"+u.Host != slackResponseOrigin {
		log.Printf("[ChatOpsPlugin] ignoring response_url outside %s", slackResponseOrigin)
		return
	}
	body, err := json.Marshal(map[string]interface{}{
		"replace_original": true,
		"text":             text,
	})
	if err != nil {
		return
	}
	req, err := http.NewRequest(http.MethodPost, responseURL, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		log.Printf("[ChatOpsPlugin] slack response_url post failed: %v", err)
		return
	}
	defer resp.Body.Close()
}
