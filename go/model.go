package flags

// The flag definition wire shapes — the PostHog-compatible `filters` JSON that
// the console UI, the SDKs and the evaluator all speak. Definitions live in
// cloud's per-org SQLite; this package only evaluates them.

import (
	"encoding/json"
	"fmt"
)

// Def is one flag definition.
type Def struct {
	Key     string  `json:"key"`
	Active  bool    `json:"active"`
	Deleted bool    `json:"deleted"`
	Filters Filters `json:"filters"`
	// AggregationGroupTypeIndex, when set, aggregates the flag by a group type
	// (its index) instead of by the person.
	AggregationGroupTypeIndex *int `json:"aggregation_group_type_index,omitempty"`
}

// UnmarshalJSON decodes a definition, defaulting `active` to true when the key
// is absent — the wire contract a stored definition relies on.
func (d *Def) UnmarshalJSON(b []byte) error {
	type plain Def // shed the method so encoding/json does the fields
	v := plain{Active: true}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*d = Def(v)
	return nil
}

type Filters struct {
	Groups       []ConditionGroup `json:"groups,omitempty"`
	Multivariate *Multivariate    `json:"multivariate,omitempty"`
	// Payloads maps "true" -> the payload for a boolean match, or a variant key
	// -> that variant's payload.
	Payloads map[string]json.RawMessage `json:"payloads,omitempty"`
}

// ConditionGroup is one release condition: property filters plus a rollout.
type ConditionGroup struct {
	Properties []PropertyFilter `json:"properties,omitempty"`
	// RolloutPercentage nil means 100%.
	RolloutPercentage *float64 `json:"rollout_percentage,omitempty"`
	// Variant pins a specific variant when this group matches (it must exist in
	// Multivariate to take effect).
	Variant *string `json:"variant,omitempty"`
}

type Multivariate struct {
	Variants []Variant `json:"variants,omitempty"`
}

type Variant struct {
	Key               string  `json:"key"`
	RolloutPercentage float64 `json:"rollout_percentage"`
}

// PropertyFilter is one property condition inside a condition group. Value is
// absent (nil) for the is_set / is_not_set operators, and for filters the API
// built without one.
type PropertyFilter struct {
	Key            string          `json:"key"`
	Value          json.RawMessage `json:"value,omitempty"`
	Operator       *Operator       `json:"operator,omitempty"`
	Type           PropertyType    `json:"type"`
	Negation       *bool           `json:"negation,omitempty"`
	GroupTypeIndex *int            `json:"group_type_index,omitempty"`
}

// Operator is the comparison a property filter applies.
type Operator string

const (
	OpExact           Operator = "exact"
	OpIsNot           Operator = "is_not"
	OpIcontains       Operator = "icontains"
	OpNotIcontains    Operator = "not_icontains"
	OpRegex           Operator = "regex"
	OpNotRegex        Operator = "not_regex"
	OpGt              Operator = "gt"
	OpLt              Operator = "lt"
	OpGte             Operator = "gte"
	OpLte             Operator = "lte"
	OpSemverGt        Operator = "semver_gt"
	OpSemverGte       Operator = "semver_gte"
	OpSemverLt        Operator = "semver_lt"
	OpSemverLte       Operator = "semver_lte"
	OpSemverEq        Operator = "semver_eq"
	OpSemverNeq       Operator = "semver_neq"
	OpSemverTilde     Operator = "semver_tilde"
	OpSemverCaret     Operator = "semver_caret"
	OpSemverWildcard  Operator = "semver_wildcard"
	OpIsSet           Operator = "is_set"
	OpIsNotSet        Operator = "is_not_set"
	OpIsDateExact     Operator = "is_date_exact"
	OpIsDateAfter     Operator = "is_date_after"
	OpIsDateBefore    Operator = "is_date_before"
	OpIn              Operator = "in"
	OpNotIn           Operator = "not_in"
	OpFlagEvaluatesTo Operator = "flag_evaluates_to"
)

var operators = map[Operator]bool{
	OpExact: true, OpIsNot: true, OpIcontains: true, OpNotIcontains: true,
	OpRegex: true, OpNotRegex: true, OpGt: true, OpLt: true, OpGte: true, OpLte: true,
	OpSemverGt: true, OpSemverGte: true, OpSemverLt: true, OpSemverLte: true,
	OpSemverEq: true, OpSemverNeq: true, OpSemverTilde: true, OpSemverCaret: true,
	OpSemverWildcard: true, OpIsSet: true, OpIsNotSet: true, OpIsDateExact: true,
	OpIsDateAfter: true, OpIsDateBefore: true, OpIn: true, OpNotIn: true,
	OpFlagEvaluatesTo: true,
}

// UnmarshalJSON rejects unknown operators so a definition carrying one fails to
// parse rather than silently never matching.
func (o *Operator) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if !operators[Operator(s)] {
		return fmt.Errorf("unknown variant `%s`", s)
	}
	*o = Operator(s)
	return nil
}

// PropertyType is which bag a property filter reads from.
type PropertyType string

const (
	TypePerson PropertyType = "person"
	TypeCohort PropertyType = "cohort"
	TypeGroup  PropertyType = "group"
	// TypeFlag compares against another flag's evaluation result.
	TypeFlag PropertyType = "flag"
)

// UnmarshalJSON rejects unknown property types for the same reason as Operator.
func (p *PropertyType) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	switch PropertyType(s) {
	case TypePerson, TypeCohort, TypeGroup, TypeFlag:
		*p = PropertyType(s)
		return nil
	}
	return fmt.Errorf("unknown variant `%s`", s)
}
