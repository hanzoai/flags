package flags

import (
	"encoding/json"
	"testing"
)

// defsOf decodes a definitions literal the way a caller would.
func defsOf(t *testing.T, s string) []Def {
	t.Helper()
	var d []Def
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		t.Fatalf("decode defs: %v", err)
	}
	return d
}

func TestEvaluateTypedAPI(t *testing.T) {
	defs := defsOf(t, `[{"key":"geo","active":true,"filters":{"groups":[
		{"properties":[{"key":"plan","value":"pro","operator":"exact","type":"person"},
		               {"key":"seats","value":10,"operator":"gte","type":"person"}],
		 "rollout_percentage":100}],
		"payloads":{"true":{"cta":"Upgrade"}}}}]`)

	// Go-native property values (int, string) must behave like their JSON forms.
	got := Evaluate(defs, Context{
		DistinctID:       "user-1",
		PersonProperties: map[string]any{"plan": "pro", "seats": 12},
	})
	if !got.IsEnabled("geo") {
		t.Fatalf("expected geo enabled, got %s", got.FeatureFlags["geo"])
	}
	var payload struct {
		CTA string `json:"cta"`
	}
	if !got.Payload("geo", &payload) || payload.CTA != "Upgrade" {
		t.Fatalf("payload not delivered: %+v", got.FeatureFlagPayloads)
	}

	if off := Evaluate(defs, Context{DistinctID: "user-1", PersonProperties: map[string]any{"plan": "free", "seats": 12}}); off.IsEnabled("geo") {
		t.Fatal("free plan should not match")
	}
	if off := Evaluate(defs, Context{DistinctID: "user-1", PersonProperties: map[string]any{"plan": "pro", "seats": 2}}); off.IsEnabled("geo") {
		t.Fatal("seats below the threshold should not match")
	}
}

func TestEvaluateIsDeterministic(t *testing.T) {
	defs := defsOf(t, `[{"key":"exp","active":true,"filters":{
		"groups":[{"properties":[],"rollout_percentage":100}],
		"multivariate":{"variants":[{"key":"control","rollout_percentage":50},{"key":"treatment","rollout_percentage":50}]}}}]`)

	first := Evaluate(defs, Context{DistinctID: "sticky-user"}).Variant("exp")
	if first != "control" && first != "treatment" {
		t.Fatalf("unexpected variant %q", first)
	}
	for i := 0; i < 50; i++ {
		if v := Evaluate(defs, Context{DistinctID: "sticky-user"}).Variant("exp"); v != first {
			t.Fatalf("variant drifted: %q then %q", first, v)
		}
	}
}

func TestEvaluateGroupAggregation(t *testing.T) {
	defs := defsOf(t, `[{"key":"org","active":true,"aggregation_group_type_index":0,"filters":{"groups":[
		{"properties":[{"key":"tier","value":"enterprise","operator":"exact","type":"group","group_type_index":0}],"rollout_percentage":100}]}}]`)

	on := Evaluate(defs, Context{DistinctID: "u", Groups: map[string]GroupCtx{
		"0": {Key: "acme", Properties: map[string]any{"tier": "enterprise"}},
	}})
	if !on.IsEnabled("org") {
		t.Fatal("group flag should match its group's properties")
	}
	// A group the caller did not supply is a plain miss, not a computation error.
	absent := Evaluate(defs, Context{DistinctID: "u"})
	if absent.IsEnabled("org") || absent.ErrorsWhileComputingFlags {
		t.Fatalf("absent group should be off and clean: %+v", absent)
	}
}

func TestEvaluateInconclusiveNeverGuesses(t *testing.T) {
	// A cohort filter needs a store this engine does not have. The flag must be
	// omitted entirely so callers fall back, rather than reported as false.
	defs := defsOf(t, `[{"key":"cohorted","active":true,"filters":{"groups":[{"properties":[{"key":"id","value":42,"type":"cohort"}]}]}}]`)
	got := Evaluate(defs, Context{DistinctID: "u"})
	if _, present := got.FeatureFlags["cohorted"]; present {
		t.Fatal("an inconclusive flag must not appear in the result")
	}
	if !got.ErrorsWhileComputingFlags {
		t.Fatal("an inconclusive flag must set ErrorsWhileComputingFlags")
	}
}

func TestEvaluateJSONRejectsMalformedInput(t *testing.T) {
	for _, tc := range []struct{ name, defs, ctx string }{
		{"bad defs", `not json`, `{"distinct_id":"u"}`},
		{"bad ctx", `[]`, `not json`},
		{"missing distinct_id", `[]`, `{}`},
		{"unknown operator", `[{"key":"k","filters":{"groups":[{"properties":[{"key":"a","value":1,"operator":"nope","type":"person"}]}]}}]`, `{"distinct_id":"u"}`},
		{"unknown property type", `[{"key":"k","filters":{"groups":[{"properties":[{"key":"a","value":1,"type":"nope"}]}]}}]`, `{"distinct_id":"u"}`},
	} {
		if _, err := EvaluateJSON([]byte(tc.defs), []byte(tc.ctx)); err == nil {
			t.Errorf("%s: expected an error", tc.name)
		}
	}
	// An empty distinct_id is valid — only an absent one is not.
	if _, err := EvaluateJSON([]byte(`[{"key":"k"}]`), []byte(`{"distinct_id":""}`)); err != nil {
		t.Errorf("empty distinct_id should evaluate: %v", err)
	}
}

func TestRolloutIsStableAndProportional(t *testing.T) {
	defs := defsOf(t, `[{"key":"half","active":true,"filters":{"groups":[{"properties":[],"rollout_percentage":50}]}}]`)
	on := 0
	for i := 0; i < 2000; i++ {
		id := "user-" + string(rune('a'+i%26)) + itoa(i)
		if Evaluate(defs, Context{DistinctID: id}).IsEnabled("half") {
			on++
		}
	}
	if on < 900 || on > 1100 {
		t.Fatalf("50%% rollout landed %d/2000 — the hash is not uniform", on)
	}
}

func itoa(i int) string {
	b, _ := json.Marshal(i)
	return string(b)
}
