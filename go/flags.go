// Package flags is the Go client for the Hanzo native flags engine —
// cloud's /v1/flags (stateless Rust evaluator, per-org/project SQLite
// definitions, PostHog-compatible semantics).
//
//	c := flags.New("https://api.hanzo.ai", flags.WithToken(os.Getenv("HANZO_API_KEY")))
//	res, _ := c.Evaluate(ctx, flags.Context{DistinctID: "user-42",
//		PersonProperties: map[string]any{"plan": "pro"}})
//	if res.IsEnabled("new-nav") { ... }
//	switch res.Variant("checkout-exp") { case "treatment": ... }
package flags

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client evaluates flags over POST {host}/v1/flags.
type Client struct {
	host    string
	token   string
	project string
	hc      *http.Client
}

type Option func(*Client)

// WithToken sets the bearer token (server-side callers).
func WithToken(t string) Option { return func(c *Client) { c.token = t } }

// WithProject scopes evaluation to a project (X-Project-Id).
func WithProject(p string) Option { return func(c *Client) { c.project = p } }

// WithHTTPClient overrides the HTTP client (default 5s timeout).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.hc = h } }

func New(host string, opts ...Option) *Client {
	c := &Client{host: strings.TrimRight(host, "/"), hc: &http.Client{Timeout: 5 * time.Second}}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Context is one evaluation identity.
type Context struct {
	DistinctID       string              `json:"distinct_id"`
	PersonProperties map[string]any      `json:"person_properties,omitempty"`
	Groups           map[string]GroupCtx `json:"groups,omitempty"` // group_type_index -> identity
}

type GroupCtx struct {
	Key        string         `json:"key"`
	Properties map[string]any `json:"properties,omitempty"`
}

// Result is the PostHog-shaped evaluation response.
type Result struct {
	FeatureFlags              map[string]json.RawMessage `json:"featureFlags"`
	FeatureFlagPayloads       map[string]json.RawMessage `json:"featureFlagPayloads"`
	ErrorsWhileComputingFlags bool                       `json:"errorsWhileComputingFlags"`
}

// IsEnabled is true for boolean true or any variant string.
func (r Result) IsEnabled(key string) bool {
	raw, ok := r.FeatureFlags[key]
	if !ok {
		return false
	}
	if string(raw) == "true" {
		return true
	}
	var s string
	return json.Unmarshal(raw, &s) == nil && s != ""
}

// Variant returns the variant key for multivariate flags, or "".
func (r Result) Variant(key string) string {
	var s string
	if raw, ok := r.FeatureFlags[key]; ok && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// Payload unmarshals the flag's payload into v; false when absent.
func (r Result) Payload(key string, v any) bool {
	raw, ok := r.FeatureFlagPayloads[key]
	if !ok {
		return false
	}
	return json.Unmarshal(raw, v) == nil
}

// Evaluate runs the caller's flags for one identity.
func (c *Client) Evaluate(ctx context.Context, ec Context) (Result, error) {
	body, err := json.Marshal(ec)
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host+"/v1/flags", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.project != "" {
		req.Header.Set("X-Project-Id", c.project)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return Result{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return Result{}, fmt.Errorf("flags: status %d", resp.StatusCode)
	}
	var out Result
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Result{}, err
	}
	return out, nil
}
