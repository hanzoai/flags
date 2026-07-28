package flags

// The stateless evaluator: (definitions, context) -> result. Pure, no IO, no
// state, no clock beyond what the date operators parse — trivially parallel and
// safe to call on every request.
//
//   - a flag matches on the FIRST condition group that passes; groups pinning a
//     variant are considered first, so an override can win
//   - a group passes when ALL its property filters match AND the rollout hash
//     clears its percentage (absent means 100)
//   - person flags hash the distinct id; group flags hash that group's key and
//     read that group's properties
//   - variants come from the matched group's override when it names a real one,
//     otherwise from a cumulative walk of the multivariate table
//   - cohort and flag-dependency filters need stores this engine deliberately
//     does not have: the flag is omitted and ErrorsWhileComputingFlags is set,
//     so callers keep their own fallback rather than being told a wrong answer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
)

// Evaluate runs a set of flag definitions for one identity.
func Evaluate(defs []Def, ctx Context) Result {
	out := Result{
		FeatureFlags:        map[string]json.RawMessage{},
		FeatureFlagPayloads: map[string]json.RawMessage{},
	}
	person := propsOf(ctx.PersonProperties)
	for _, d := range defs {
		if d.Deleted {
			continue
		}
		if !d.Active {
			out.FeatureFlags[d.Key] = rawFalse
			continue
		}
		on, variant, inconclusive := evaluateFlag(d, ctx, person)
		switch {
		case inconclusive:
			out.ErrorsWhileComputingFlags = true
		case !on:
			out.FeatureFlags[d.Key] = rawFalse
		default:
			payloadKey := "true"
			out.FeatureFlags[d.Key] = rawTrue
			if variant != nil {
				payloadKey = *variant
				out.FeatureFlags[d.Key] = json.RawMessage(value{k: kStr, s: *variant}.json())
			}
			if p, ok := d.Filters.Payloads[payloadKey]; ok {
				// Re-rendered rather than passed through, so a payload's numbers
				// and key order read the same here as everywhere else.
				if pv, err := parseValue(p); err == nil {
					out.FeatureFlagPayloads[d.Key] = json.RawMessage(pv.json())
				} else {
					out.FeatureFlagPayloads[d.Key] = p
				}
			}
		}
	}
	return out
}

var (
	rawTrue  = json.RawMessage("true")
	rawFalse = json.RawMessage("false")
)

func propsOf(m map[string]any) map[string]value {
	out := make(map[string]value, len(m))
	for k, v := range m {
		out[k] = fromAny(v)
	}
	return out
}

func evaluateFlag(d Def, ctx Context, person map[string]value) (on bool, variant *string, inconclusive bool) {
	// The identity the rollout hashes, and the bag the conditions read.
	identifier, props := ctx.DistinctID, person
	if d.AggregationGroupTypeIndex != nil {
		g, ok := ctx.Groups[strconv.Itoa(*d.AggregationGroupTypeIndex)]
		if !ok || g.Key == "" {
			return false, nil, false // aggregates by a group the caller did not supply
		}
		identifier, props = g.Key, propsOf(g.Properties)
	}

	// Variant-override groups first; the sort is stable so ties keep wire order.
	order := make([]ConditionGroup, len(d.Filters.Groups))
	copy(order, d.Filters.Groups)
	sort.SliceStable(order, func(i, j int) bool {
		return order[i].Variant != nil && order[j].Variant == nil
	})

	saw := false
	for _, g := range order {
		matched, bad := groupMatches(d, g, identifier, props)
		switch {
		case bad:
			saw = true
		case matched:
			return true, resolveVariant(d, g, identifier), false
		}
	}
	if saw {
		return false, nil, true
	}
	if len(d.Filters.Groups) == 0 {
		return true, nil, false // no conditions at all: gated only by `active`
	}
	return false, nil, false
}

func groupMatches(d Def, g ConditionGroup, identifier string, props map[string]value) (matched, inconclusive bool) {
	for _, f := range g.Properties {
		switch f.Type {
		case TypeCohort, TypeFlag:
			return false, true
		default:
			if !matchProperty(f, props) {
				return false, false
			}
		}
	}
	pct := 100.0
	if g.RolloutPercentage != nil {
		pct = *g.RolloutPercentage
	}
	if pct < 100 && hashOf(d.Key+".", identifier, "") > pct/100 {
		return false, false
	}
	return true, false
}

func resolveVariant(d Def, matched ConditionGroup, identifier string) *string {
	mv := d.Filters.Multivariate
	if mv == nil {
		return nil
	}
	if matched.Variant != nil {
		for _, v := range mv.Variants {
			if v.Key == *matched.Variant {
				return matched.Variant
			}
		}
	}
	h := hashOf(d.Key+".", identifier, "variant")
	cumulative := 0.0
	for i := range mv.Variants {
		cumulative += mv.Variants[i].RolloutPercentage / 100
		if h < cumulative {
			return &mv.Variants[i].Key
		}
	}
	return nil
}

// EvaluateJSON is the wire-shaped entry point: a definitions array and an
// evaluation context, both JSON, in — the PostHog-shaped response out. Numbers
// keep their literal form through the decode so property comparisons see
// exactly what the caller sent.
func EvaluateJSON(defsJSON, ctxJSON []byte) ([]byte, error) {
	var defs []Def
	if err := decode(defsJSON, &defs); err != nil {
		return nil, fmt.Errorf("bad definitions: %w", err)
	}
	// distinct_id is required to be PRESENT, though it may be empty.
	var wire struct {
		DistinctID       *string             `json:"distinct_id"`
		PersonProperties map[string]any      `json:"person_properties"`
		Groups           map[string]GroupCtx `json:"groups"`
	}
	if err := decode(ctxJSON, &wire); err != nil {
		return nil, fmt.Errorf("bad context: %w", err)
	}
	if wire.DistinctID == nil {
		return nil, fmt.Errorf("bad context: missing field `distinct_id`")
	}
	ctx := Context{DistinctID: *wire.DistinctID, PersonProperties: wire.PersonProperties, Groups: wire.Groups}
	// Encoded with HTML escaping off: the reference emits < > & literally, and
	// encoding/json would not.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(Evaluate(defs, ctx)); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func decode(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	return dec.Decode(v)
}
