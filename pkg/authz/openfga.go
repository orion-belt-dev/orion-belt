package authz

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/orion-belt-dev/orion-belt/pkg/common"
)

// Authorizer decides whether a user may access a machine.
type Authorizer interface {
	Check(ctx context.Context, userID, machineID, accessType string) (bool, error)
	WriteGrant(ctx context.Context, userID, machineID, accessType string) error
	DeleteGrant(ctx context.Context, userID, machineID, accessType string) error
}

// OpenFGA is an HTTP client for OpenFGA Check/Write/Delete.
type OpenFGA struct {
	apiURL   string
	storeID  string
	modelID  string
	token    string
	relation string
	client   *http.Client
	logger   *common.Logger
}

// NewOpenFGA creates an OpenFGA client from config. Returns nil if disabled.
func NewOpenFGA(cfg common.OpenFGAConfig, logger *common.Logger) (*OpenFGA, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.APIURL == "" || cfg.StoreID == "" {
		return nil, fmt.Errorf("openfga enabled but api_url/store_id missing")
	}
	rel := cfg.Relation
	if rel == "" {
		rel = "can_access"
	}
	return &OpenFGA{
		apiURL:   strings.TrimRight(cfg.APIURL, "/"),
		storeID:  cfg.StoreID,
		modelID:  cfg.ModelID,
		token:    cfg.APIToken,
		relation: rel,
		client:   &http.Client{Timeout: 10 * time.Second},
		logger:   logger,
	}, nil
}

func (o *OpenFGA) Check(ctx context.Context, userID, machineID, accessType string) (bool, error) {
	body := map[string]interface{}{
		"tuple_key": map[string]string{
			"user":     "user:" + userID,
			"relation": o.relation,
			"object":   "machine:" + machineID,
		},
		"context": map[string]string{
			"access_type": accessType,
		},
	}
	if o.modelID != "" {
		body["authorization_model_id"] = o.modelID
	}

	var resp struct {
		Allowed bool `json:"allowed"`
	}
	if err := o.post(ctx, fmt.Sprintf("/stores/%s/check", o.storeID), body, &resp); err != nil {
		return false, err
	}
	return resp.Allowed, nil
}

func (o *OpenFGA) WriteGrant(ctx context.Context, userID, machineID, accessType string) error {
	tuple := map[string]string{
		"user":     "user:" + userID,
		"relation": o.relation,
		"object":   "machine:" + machineID,
	}
	body := map[string]interface{}{
		"writes": map[string]interface{}{
			"tuple_keys": []map[string]string{tuple},
		},
	}
	if o.modelID != "" {
		body["authorization_model_id"] = o.modelID
	}
	return o.post(ctx, fmt.Sprintf("/stores/%s/write", o.storeID), body, nil)
}

func (o *OpenFGA) DeleteGrant(ctx context.Context, userID, machineID, accessType string) error {
	tuple := map[string]string{
		"user":     "user:" + userID,
		"relation": o.relation,
		"object":   "machine:" + machineID,
	}
	body := map[string]interface{}{
		"deletes": map[string]interface{}{
			"tuple_keys": []map[string]string{tuple},
		},
	}
	if o.modelID != "" {
		body["authorization_model_id"] = o.modelID
	}
	return o.post(ctx, fmt.Sprintf("/stores/%s/write", o.storeID), body, nil)
}

func (o *OpenFGA) post(ctx context.Context, path string, body interface{}, out interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.apiURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.token != "" {
		req.Header.Set("Authorization", "Bearer "+o.token)
	}
	resp, err := o.client.Do(req)
	if err != nil {
		return fmt.Errorf("openfga request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("openfga status %d: %s", resp.StatusCode, string(raw))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}
