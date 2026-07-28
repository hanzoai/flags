package flags

// Parity against the Rust evaluator this package replaced.
//
// The rollout hash decides which identities fall inside a percentage, so a Go
// answer that differs from the reference by even one identity splits a cohort
// differently than the rest of the fleet. testdata/oracle.json is that
// reference's recorded answer for every case below — produced by running the
// Rust staticlib over testdata/cases.json — and this test is the standing proof
// that the port still agrees with it.
//
// Regenerate after changing the case table:
//
//	go test ./... -run TestWriteParityCases -write-cases
//	oracle testdata/cases.json > testdata/oracle.json
//
// where `oracle` links the Rust crate and calls hanzo_flags::evaluate.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var writeCases = flag.Bool("write-cases", false, "write testdata/cases.json for the Rust oracle")

type parityCase struct {
	Name string          `json:"name"`
	Defs json.RawMessage `json:"defs"`
	Ctx  json.RawMessage `json:"ctx"`
}

func newCase(name, defs, ctx string) parityCase {
	return parityCase{Name: name, Defs: json.RawMessage(defs), Ctx: json.RawMessage(ctx)}
}

// person wraps a property bag into an evaluation context.
func person(id, props string) string {
	if props == "" {
		return fmt.Sprintf(`{"distinct_id":%q}`, id)
	}
	return fmt.Sprintf(`{"distinct_id":%q,"person_properties":%s}`, id, props)
}

// oneFilter builds a single-condition flag carrying one property filter.
func oneFilter(filter string) string {
	return `[{"key":"f","active":true,"filters":{"groups":[{"properties":[` + filter + `],"rollout_percentage":100}]}}]`
}

func parityCases() []parityCase {
	var cs []parityCase

	// ── rollout bucketing ────────────────────────────────────────────────────
	// Every percentage against many identities: this is what proves the sha1
	// bucketing agrees identity by identity, not just in aggregate.
	for _, pct := range []string{"0", "1", "25", "50", "99", "100", "33.3", "0.5"} {
		for i := 0; i < 40; i++ {
			cs = append(cs, newCase(
				fmt.Sprintf("rollout-%s-user-%d", pct, i),
				fmt.Sprintf(`[{"key":"roll","active":true,"filters":{"groups":[{"properties":[],"rollout_percentage":%s}]}}]`, pct),
				person(fmt.Sprintf("user-%d", i), ""),
			))
		}
	}
	// Rollout with no percentage at all (means 100), and unusual identities.
	for _, id := range []string{"", "0", "user@example.com", "ünïcødé-id", "a very long identity string that keeps going and going", "💡emoji", `quote"inside`} {
		cs = append(cs, newCase("rollout-absent-"+id,
			`[{"key":"roll","active":true,"filters":{"groups":[{"properties":[]}]}}]`,
			person(id, "")))
		cs = append(cs, newCase("rollout-50-"+id,
			`[{"key":"roll","active":true,"filters":{"groups":[{"properties":[],"rollout_percentage":50}]}}]`,
			person(id, "")))
	}

	// ── multivariate bucketing ───────────────────────────────────────────────
	mv := `"multivariate":{"variants":[{"key":"control","rollout_percentage":34},{"key":"test-a","rollout_percentage":33},{"key":"test-b","rollout_percentage":33}]}`
	for i := 0; i < 40; i++ {
		cs = append(cs, newCase(fmt.Sprintf("variant-3way-user-%d", i),
			`[{"key":"exp","active":true,"filters":{"groups":[{"properties":[],"rollout_percentage":100}],`+mv+`}}]`,
			person(fmt.Sprintf("user-%d", i), "")))
	}
	// Uneven and degenerate splits, including one that does not reach 100.
	for _, split := range []struct{ name, variants string }{
		{"even-2", `[{"key":"a","rollout_percentage":50},{"key":"b","rollout_percentage":50}]`},
		{"skew-90-10", `[{"key":"a","rollout_percentage":90},{"key":"b","rollout_percentage":10}]`},
		{"short-30", `[{"key":"a","rollout_percentage":30}]`},
		{"zero-first", `[{"key":"a","rollout_percentage":0},{"key":"b","rollout_percentage":100}]`},
		{"empty", `[]`},
	} {
		for i := 0; i < 12; i++ {
			cs = append(cs, newCase(fmt.Sprintf("variant-%s-user-%d", split.name, i),
				`[{"key":"exp","active":true,"filters":{"groups":[{"properties":[],"rollout_percentage":100}],"multivariate":{"variants":`+split.variants+`}}}]`,
				person(fmt.Sprintf("user-%d", i), "")))
		}
	}
	// Variant override: valid, invalid, and one that loses to group ordering.
	cs = append(cs,
		newCase("variant-override-valid",
			`[{"key":"exp","active":true,"filters":{"groups":[{"properties":[{"key":"vip","value":true,"operator":"exact","type":"person"}],"rollout_percentage":100,"variant":"test-a"},{"properties":[],"rollout_percentage":100}],`+mv+`}}]`,
			person("vip-user", `{"vip":true}`)),
		newCase("variant-override-unknown-key",
			`[{"key":"exp","active":true,"filters":{"groups":[{"properties":[],"rollout_percentage":100,"variant":"nope"}],`+mv+`}}]`,
			person("u", "")),
		newCase("variant-override-sorted-first",
			`[{"key":"exp","active":true,"filters":{"groups":[{"properties":[],"rollout_percentage":100},{"properties":[],"rollout_percentage":100,"variant":"test-b"}],`+mv+`}}]`,
			person("u", "")),
		newCase("variant-override-no-multivariate",
			`[{"key":"exp","active":true,"filters":{"groups":[{"properties":[],"rollout_percentage":100,"variant":"test-a"}]}}]`,
			person("u", "")),
	)

	// ── payloads ─────────────────────────────────────────────────────────────
	cs = append(cs,
		newCase("payload-boolean",
			`[{"key":"p","active":true,"filters":{"groups":[{"properties":[],"rollout_percentage":100}],"payloads":{"true":{"cta":"buy","n":1,"f":1.5,"list":[1,2,3]}}}}]`,
			person("u", "")),
		newCase("payload-per-variant",
			`[{"key":"p","active":true,"filters":{"groups":[{"properties":[],"rollout_percentage":100}],`+mv+`,"payloads":{"control":{"a":1},"test-a":"plain string","test-b":[1,2]}}}]`,
			person("u", "")),
		newCase("payload-number-forms",
			`[{"key":"p","active":true,"filters":{"groups":[{"properties":[],"rollout_percentage":100}],"payloads":{"true":{"a":1,"b":1.0,"c":1.50,"d":1e3,"e":1e100,"g":-0.0,"h":9007199254740993,"i":2.5e10,"j":1.5e-8,"k":0.1}}}}]`,
			person("u", "")),
		newCase("payload-string-escapes",
			`[{"key":"p","active":true,"filters":{"groups":[{"properties":[],"rollout_percentage":100}],"payloads":{"true":{"html":"<b>&amp;</b>","quote":"say \"hi\"","tab":"a\tb","nl":"a\nb","uni":"ünïcødé 💡","slash":"a/b"}}}}]`,
			person("u", "")),
		newCase("payload-missing-for-variant",
			`[{"key":"p","active":true,"filters":{"groups":[{"properties":[],"rollout_percentage":100}],`+mv+`,"payloads":{"true":{"unused":true}}}}]`,
			person("u", "")),
	)

	// ── property operators ───────────────────────────────────────────────────
	type opCase struct{ name, filter, props string }
	ops := []opCase{
		// exact
		{"exact-hit", `{"key":"c","value":"DE","operator":"exact","type":"person"}`, `{"c":"DE"}`},
		{"exact-miss", `{"key":"c","value":"DE","operator":"exact","type":"person"}`, `{"c":"US"}`},
		{"exact-case-insensitive", `{"key":"c","value":"de","operator":"exact","type":"person"}`, `{"c":"DE"}`},
		{"exact-absent-property", `{"key":"c","value":"DE","operator":"exact","type":"person"}`, `{"other":"DE"}`},
		{"exact-null-property", `{"key":"c","value":"DE","operator":"exact","type":"person"}`, `{"c":null}`},
		{"exact-default-operator", `{"key":"c","value":"DE","type":"person"}`, `{"c":"DE"}`},
		{"exact-array-hit", `{"key":"c","value":["DE","FR"],"operator":"exact","type":"person"}`, `{"c":"FR"}`},
		{"exact-array-miss", `{"key":"c","value":["DE","FR"],"operator":"exact","type":"person"}`, `{"c":"US"}`},
		{"exact-array-case", `{"key":"c","value":["de","fr"],"operator":"exact","type":"person"}`, `{"c":"FR"}`},
		{"exact-bool-true", `{"key":"c","value":true,"operator":"exact","type":"person"}`, `{"c":true}`},
		{"exact-bool-string", `{"key":"c","value":"True","operator":"exact","type":"person"}`, `{"c":true}`},
		{"exact-bool-vs-false", `{"key":"c","value":true,"operator":"exact","type":"person"}`, `{"c":false}`},
		{"exact-bool-vs-string", `{"key":"c","value":"false","operator":"exact","type":"person"}`, `{"c":"FALSE"}`},
		{"exact-bool-vs-other", `{"key":"c","value":true,"operator":"exact","type":"person"}`, `{"c":"yes"}`},
		{"exact-empty-array-filter", `{"key":"c","value":[],"operator":"exact","type":"person"}`, `{"c":"anything"}`},
		{"exact-number", `{"key":"n","value":42,"operator":"exact","type":"person"}`, `{"n":42}`},
		{"exact-number-vs-string", `{"key":"n","value":"42","operator":"exact","type":"person"}`, `{"n":42}`},
		{"exact-object", `{"key":"o","value":{"b":2,"a":1},"operator":"exact","type":"person"}`, `{"o":{"a":1,"b":2}}`},
		{"exact-no-value", `{"key":"c","operator":"exact","type":"person"}`, `{"c":"DE"}`},
		// numeric rendering through exact: the filter is a string, so the
		// property's rendered form is what decides the match
		{"render-int", `{"key":"n","value":"1","operator":"exact","type":"person"}`, `{"n":1}`},
		{"render-float-whole", `{"key":"n","value":"1.0","operator":"exact","type":"person"}`, `{"n":1.0}`},
		{"render-float-trailing-zero", `{"key":"n","value":"1.5","operator":"exact","type":"person"}`, `{"n":1.50}`},
		{"render-exp-positive", `{"key":"n","value":"1000.0","operator":"exact","type":"person"}`, `{"n":1e3}`},
		{"render-exp-big", `{"key":"n","value":"1e+100","operator":"exact","type":"person"}`, `{"n":1e100}`},
		{"render-exp-small", `{"key":"n","value":"1.5e-8","operator":"exact","type":"person"}`, `{"n":1.5e-8}`},
		{"render-neg-zero", `{"key":"n","value":"-0.0","operator":"exact","type":"person"}`, `{"n":-0.0}`},
		{"render-big-int", `{"key":"n","value":"9007199254740993","operator":"exact","type":"person"}`, `{"n":9007199254740993}`},
		{"render-huge-int", `{"key":"n","value":"1.2345678901234568e+29","operator":"exact","type":"person"}`, `{"n":123456789012345678901234567890}`},
		{"render-positional-big", `{"key":"n","value":"25000000000.0","operator":"exact","type":"person"}`, `{"n":2.5e10}`},
		{"render-object", `{"key":"o","value":"{\"a\":1,\"b\":[1,2]}","operator":"exact","type":"person"}`, `{"o":{"b":[1,2],"a":1}}`},
		// is_not
		{"is-not-hit", `{"key":"c","value":"DE","operator":"is_not","type":"person"}`, `{"c":"US"}`},
		{"is-not-miss", `{"key":"c","value":"DE","operator":"is_not","type":"person"}`, `{"c":"DE"}`},
		{"is-not-array", `{"key":"c","value":["DE","FR"],"operator":"is_not","type":"person"}`, `{"c":"US"}`},
		{"is-not-absent", `{"key":"c","value":"DE","operator":"is_not","type":"person"}`, `{"x":1}`},
		// icontains
		{"icontains-hit", `{"key":"e","value":"@hanzo.ai","operator":"icontains","type":"person"}`, `{"e":"Z@Hanzo.AI"}`},
		{"icontains-miss", `{"key":"e","value":"@hanzo.ai","operator":"icontains","type":"person"}`, `{"e":"z@example.com"}`},
		{"icontains-number", `{"key":"n","value":"23","operator":"icontains","type":"person"}`, `{"n":12345}`},
		{"icontains-exp", `{"key":"n","value":"e+","operator":"icontains","type":"person"}`, `{"n":1e100}`},
		{"icontains-unicode", `{"key":"e","value":"CØDÉ","operator":"icontains","type":"person"}`, `{"e":"ünïcødé"}`},
		{"not-icontains-hit", `{"key":"e","value":"@hanzo.ai","operator":"not_icontains","type":"person"}`, `{"e":"z@example.com"}`},
		{"not-icontains-miss", `{"key":"e","value":"@hanzo.ai","operator":"not_icontains","type":"person"}`, `{"e":"z@hanzo.ai"}`},
		// regex
		{"regex-hit", `{"key":"e","value":"^z@.*\\.ai$","operator":"regex","type":"person"}`, `{"e":"z@hanzo.ai"}`},
		{"regex-miss", `{"key":"e","value":"^z@.*\\.ai$","operator":"regex","type":"person"}`, `{"e":"a@hanzo.com"}`},
		{"regex-unanchored", `{"key":"e","value":"hanzo","operator":"regex","type":"person"}`, `{"e":"z@hanzo.ai"}`},
		{"regex-classes", `{"key":"v","value":"^[0-9]{3}-[a-z]+$","operator":"regex","type":"person"}`, `{"v":"123-abc"}`},
		{"regex-case-sensitive", `{"key":"v","value":"ABC","operator":"regex","type":"person"}`, `{"v":"abc"}`},
		{"regex-inline-flag", `{"key":"v","value":"(?i)ABC","operator":"regex","type":"person"}`, `{"v":"abc"}`},
		{"regex-invalid", `{"key":"v","value":"[unclosed","operator":"regex","type":"person"}`, `{"v":"anything"}`},
		{"regex-on-number", `{"key":"n","value":"^4","operator":"regex","type":"person"}`, `{"n":42}`},
		{"not-regex-hit", `{"key":"e","value":"^z@","operator":"not_regex","type":"person"}`, `{"e":"a@hanzo.ai"}`},
		{"not-regex-miss", `{"key":"e","value":"^z@","operator":"not_regex","type":"person"}`, `{"e":"z@hanzo.ai"}`},
		{"not-regex-invalid", `{"key":"v","value":"[unclosed","operator":"not_regex","type":"person"}`, `{"v":"anything"}`},
		// comparisons
		{"gt-hit", `{"key":"n","value":10,"operator":"gt","type":"person"}`, `{"n":11}`},
		{"gt-miss", `{"key":"n","value":10,"operator":"gt","type":"person"}`, `{"n":10}`},
		{"gte-hit", `{"key":"n","value":10,"operator":"gte","type":"person"}`, `{"n":10}`},
		{"lt-hit", `{"key":"n","value":10,"operator":"lt","type":"person"}`, `{"n":9.5}`},
		{"lte-hit", `{"key":"n","value":10,"operator":"lte","type":"person"}`, `{"n":10}`},
		{"gt-string-numbers", `{"key":"n","value":"10","operator":"gt","type":"person"}`, `{"n":"11"}`},
		{"gt-non-numeric-property", `{"key":"n","value":10,"operator":"gt","type":"person"}`, `{"n":"abc"}`},
		{"gt-non-numeric-filter", `{"key":"n","value":"abc","operator":"gt","type":"person"}`, `{"n":10}`},
		{"gt-bool-property", `{"key":"n","value":0,"operator":"gt","type":"person"}`, `{"n":true}`},
		{"gt-negative", `{"key":"n","value":-5,"operator":"gt","type":"person"}`, `{"n":-1}`},
		{"gt-exponent-string", `{"key":"n","value":"1e2","operator":"gt","type":"person"}`, `{"n":150}`},
		// is_set / is_not_set
		{"is-set-present", `{"key":"c","operator":"is_set","type":"person"}`, `{"c":"DE"}`},
		{"is-set-null", `{"key":"c","operator":"is_set","type":"person"}`, `{"c":null}`},
		{"is-set-absent", `{"key":"c","operator":"is_set","type":"person"}`, `{"x":1}`},
		{"is-not-set-present", `{"key":"c","operator":"is_not_set","type":"person"}`, `{"c":"DE"}`},
		{"is-not-set-absent", `{"key":"c","operator":"is_not_set","type":"person"}`, `{"x":1}`},
		// semver comparisons
		{"semver-gt-hit", `{"key":"v","value":"1.2.3","operator":"semver_gt","type":"person"}`, `{"v":"1.3.0"}`},
		{"semver-gt-miss", `{"key":"v","value":"1.2.3","operator":"semver_gt","type":"person"}`, `{"v":"1.2.3"}`},
		{"semver-gte-hit", `{"key":"v","value":"1.2.3","operator":"semver_gte","type":"person"}`, `{"v":"1.2.3"}`},
		{"semver-lt-hit", `{"key":"v","value":"1.2.3","operator":"semver_lt","type":"person"}`, `{"v":"1.2.2"}`},
		{"semver-lte-hit", `{"key":"v","value":"1.2.3","operator":"semver_lte","type":"person"}`, `{"v":"1.2.3"}`},
		{"semver-eq-hit", `{"key":"v","value":"1.2.3","operator":"semver_eq","type":"person"}`, `{"v":"1.2.3"}`},
		{"semver-neq-hit", `{"key":"v","value":"1.2.3","operator":"semver_neq","type":"person"}`, `{"v":"1.2.4"}`},
		{"semver-v-prefix", `{"key":"v","value":"v1.2.3","operator":"semver_gte","type":"person"}`, `{"v":"v1.2.3"}`},
		{"semver-whitespace", `{"key":"v","value":"1.2.3 ","operator":"semver_eq","type":"person"}`, `{"v":" 1.2.3"}`},
		{"semver-prerelease-order", `{"key":"v","value":"1.2.3-alpha","operator":"semver_lt","type":"person"}`, `{"v":"1.2.3-alpha.1"}`},
		{"semver-prerelease-vs-release", `{"key":"v","value":"1.2.3","operator":"semver_lt","type":"person"}`, `{"v":"1.2.3-alpha"}`},
		{"semver-numeric-ident", `{"key":"v","value":"1.2.3-2","operator":"semver_lt","type":"person"}`, `{"v":"1.2.3-11"}`},
		{"semver-build-metadata-eq", `{"key":"v","value":"1.2.3+build.1","operator":"semver_eq","type":"person"}`, `{"v":"1.2.3+build.1"}`},
		{"semver-build-metadata-neq", `{"key":"v","value":"1.2.3+build.1","operator":"semver_eq","type":"person"}`, `{"v":"1.2.3+build.2"}`},
		{"semver-build-metadata-order", `{"key":"v","value":"1.2.3+build.1","operator":"semver_lt","type":"person"}`, `{"v":"1.2.3+build.2"}`},
		{"semver-invalid-property", `{"key":"v","value":"1.2.3","operator":"semver_gt","type":"person"}`, `{"v":"not-a-version"}`},
		{"semver-partial-property", `{"key":"v","value":"1.2.3","operator":"semver_gt","type":"person"}`, `{"v":"1.2"}`},
		{"semver-leading-zero", `{"key":"v","value":"1.2.3","operator":"semver_gt","type":"person"}`, `{"v":"01.2.3"}`},
		{"semver-invalid-filter", `{"key":"v","value":"nope","operator":"semver_gt","type":"person"}`, `{"v":"1.2.3"}`},
		{"semver-major-minor", `{"key":"v","value":"1.9.9","operator":"semver_lt","type":"person"}`, `{"v":"1.10.0"}`},
		// semver ranges
		{"semver-tilde-hit", `{"key":"v","value":"1.2.3","operator":"semver_tilde","type":"person"}`, `{"v":"1.2.9"}`},
		{"semver-tilde-miss", `{"key":"v","value":"1.2.3","operator":"semver_tilde","type":"person"}`, `{"v":"1.3.0"}`},
		{"semver-tilde-below", `{"key":"v","value":"1.2.3","operator":"semver_tilde","type":"person"}`, `{"v":"1.2.2"}`},
		{"semver-tilde-prerelease", `{"key":"v","value":"1.2.3-alpha","operator":"semver_tilde","type":"person"}`, `{"v":"1.2.3-beta"}`},
		{"semver-caret-hit", `{"key":"v","value":"1.2.3","operator":"semver_caret","type":"person"}`, `{"v":"1.9.9"}`},
		{"semver-caret-major-bump", `{"key":"v","value":"1.2.3","operator":"semver_caret","type":"person"}`, `{"v":"2.0.0"}`},
		{"semver-caret-zero-major", `{"key":"v","value":"0.2.3","operator":"semver_caret","type":"person"}`, `{"v":"0.2.9"}`},
		{"semver-caret-zero-minor-bump", `{"key":"v","value":"0.2.3","operator":"semver_caret","type":"person"}`, `{"v":"0.3.0"}`},
		{"semver-caret-zero-zero", `{"key":"v","value":"0.0.3","operator":"semver_caret","type":"person"}`, `{"v":"0.0.4"}`},
		{"semver-caret-prerelease-reject", `{"key":"v","value":"1.2.3","operator":"semver_caret","type":"person"}`, `{"v":"1.3.0-alpha"}`},
		{"semver-caret-partial", `{"key":"v","value":"1.2","operator":"semver_caret","type":"person"}`, `{"v":"1.9.0"}`},
		{"semver-wildcard-patch", `{"key":"v","value":"1.2.*","operator":"semver_wildcard","type":"person"}`, `{"v":"1.2.9"}`},
		{"semver-wildcard-patch-miss", `{"key":"v","value":"1.2.*","operator":"semver_wildcard","type":"person"}`, `{"v":"1.3.0"}`},
		{"semver-wildcard-minor", `{"key":"v","value":"1.*","operator":"semver_wildcard","type":"person"}`, `{"v":"1.9.9"}`},
		{"semver-wildcard-minor-miss", `{"key":"v","value":"1.*","operator":"semver_wildcard","type":"person"}`, `{"v":"2.0.0"}`},
		{"semver-wildcard-all", `{"key":"v","value":"*","operator":"semver_wildcard","type":"person"}`, `{"v":"1.2.3"}`},
		{"semver-wildcard-all-prerelease", `{"key":"v","value":"*","operator":"semver_wildcard","type":"person"}`, `{"v":"1.2.3-alpha"}`},
		{"semver-wildcard-exact", `{"key":"v","value":"1.2.3","operator":"semver_wildcard","type":"person"}`, `{"v":"1.2.3"}`},
		{"semver-wildcard-invalid", `{"key":"v","value":"1.*.3","operator":"semver_wildcard","type":"person"}`, `{"v":"1.2.3"}`},
		// dates — only the deterministic spellings; a date-only value splices in
		// the current wall clock on both sides and cannot be compared
		{"date-before-hit", `{"key":"d","value":"2025-01-01T00:00:00Z","operator":"is_date_before","type":"person"}`, `{"d":"2024-06-01T00:00:00Z"}`},
		{"date-before-miss", `{"key":"d","value":"2025-01-01T00:00:00Z","operator":"is_date_before","type":"person"}`, `{"d":"2025-06-01T00:00:00Z"}`},
		{"date-after-hit", `{"key":"d","value":"2025-01-01T00:00:00Z","operator":"is_date_after","type":"person"}`, `{"d":"2025-06-01T00:00:00Z"}`},
		{"date-after-miss", `{"key":"d","value":"2025-01-01T00:00:00Z","operator":"is_date_after","type":"person"}`, `{"d":"2024-06-01T00:00:00Z"}`},
		{"date-exact-hit", `{"key":"d","value":"2025-01-01T00:00:00Z","operator":"is_date_exact","type":"person"}`, `{"d":"2025-01-01T00:00:00Z"}`},
		{"date-exact-miss", `{"key":"d","value":"2025-01-01T00:00:00Z","operator":"is_date_exact","type":"person"}`, `{"d":"2025-01-01T00:00:01Z"}`},
		{"date-offset-tz", `{"key":"d","value":"2025-01-01T00:00:00Z","operator":"is_date_after","type":"person"}`, `{"d":"2025-01-01T01:00:00+02:00"}`},
		{"date-fractional", `{"key":"d","value":"2025-01-01T00:00:00.500Z","operator":"is_date_after","type":"person"}`, `{"d":"2025-01-01T00:00:00.750Z"}`},
		{"date-iso-no-zone", `{"key":"d","value":"2025-01-01T00:00:00","operator":"is_date_before","type":"person"}`, `{"d":"2024-01-01T00:00:00"}`},
		{"date-iso-millis-no-zone", `{"key":"d","value":"2025-12-19T00:00:00.000","operator":"is_date_before","type":"person"}`, `{"d":"2024-01-01T00:00:00.000"}`},
		{"date-unix-seconds-string", `{"key":"d","value":"1620298830","operator":"is_date_after","type":"person"}`, `{"d":"1620299999"}`},
		{"date-unix-number", `{"key":"d","value":"1620298830","operator":"is_date_after","type":"person"}`, `{"d":1620299999}`},
		{"date-unix-float", `{"key":"d","value":"1620298830","operator":"is_date_after","type":"person"}`, `{"d":1620299999.75}`},
		{"date-relative-before", `{"key":"d","value":"-1d","operator":"is_date_before","type":"person"}`, `{"d":"2020-01-01T00:00:00Z"}`},
		{"date-relative-after", `{"key":"d","value":"-1d","operator":"is_date_after","type":"person"}`, `{"d":"2099-01-01T00:00:00Z"}`},
		{"date-relative-hours", `{"key":"d","value":"-3h","operator":"is_date_before","type":"person"}`, `{"d":"2020-01-01T00:00:00Z"}`},
		{"date-relative-weeks", `{"key":"d","value":"-2w","operator":"is_date_before","type":"person"}`, `{"d":"2020-01-01T00:00:00Z"}`},
		{"date-relative-months", `{"key":"d","value":"-6m","operator":"is_date_before","type":"person"}`, `{"d":"2020-01-01T00:00:00Z"}`},
		{"date-relative-years", `{"key":"d","value":"-2y","operator":"is_date_before","type":"person"}`, `{"d":"2020-01-01T00:00:00Z"}`},
		{"date-relative-too-large", `{"key":"d","value":"10000d","operator":"is_date_before","type":"person"}`, `{"d":"2020-01-01T00:00:00Z"}`},
		{"date-unparseable-filter", `{"key":"d","value":"not a date","operator":"is_date_before","type":"person"}`, `{"d":"2020-01-01T00:00:00Z"}`},
		{"date-unparseable-property", `{"key":"d","value":"2025-01-01T00:00:00Z","operator":"is_date_before","type":"person"}`, `{"d":"not a date"}`},
		{"date-numeric-filter-value", `{"key":"d","value":1620298830,"operator":"is_date_before","type":"person"}`, `{"d":"2020-01-01T00:00:00Z"}`},
		{"date-bool-property", `{"key":"d","value":"2025-01-01T00:00:00Z","operator":"is_date_before","type":"person"}`, `{"d":true}`},
		// cohort / flag dependencies are inconclusive by design
		{"cohort-filter", `{"key":"id","value":42,"type":"cohort"}`, `{"id":42}`},
		{"flag-filter", `{"key":"other","value":"true","operator":"flag_evaluates_to","type":"flag"}`, `{"other":"true"}`},
		{"in-operator", `{"key":"c","value":["DE"],"operator":"in","type":"person"}`, `{"c":"DE"}`},
		{"not-in-operator", `{"key":"c","value":["DE"],"operator":"not_in","type":"person"}`, `{"c":"US"}`},
	}
	for _, o := range ops {
		cs = append(cs, newCase("op-"+o.name, oneFilter(o.filter), person("u", o.props)))
	}

	// ── flag-level shapes ────────────────────────────────────────────────────
	cs = append(cs,
		newCase("flag-simple-active", `[{"key":"k","active":true}]`, person("u", "")),
		newCase("flag-active-default", `[{"key":"k"}]`, person("u", "")),
		newCase("flag-inactive", `[{"key":"k","active":false}]`, person("u", "")),
		newCase("flag-deleted", `[{"key":"k","active":true,"deleted":true}]`, person("u", "")),
		newCase("flag-empty-groups", `[{"key":"k","active":true,"filters":{"groups":[]}}]`, person("u", "")),
		newCase("flag-empty-filters", `[{"key":"k","active":true,"filters":{}}]`, person("u", "")),
		newCase("flag-none", `[]`, person("u", "")),
		newCase("flag-many", `[{"key":"a","active":true},{"key":"b","active":false},{"key":"c","active":true,"deleted":true},{"key":"d","active":true,"filters":{"groups":[{"properties":[],"rollout_percentage":50}]}},{"key":"e","active":true,"filters":{"groups":[{"properties":[{"key":"id","value":1,"type":"cohort"}]}]}}]`, person("user-7", "")),
		newCase("flag-key-escapes", `[{"key":"a/b<c>&d\"e","active":true}]`, person("u", "")),
		newCase("flag-unicode-key", `[{"key":"flag-ünïcødé-💡","active":true}]`, person("u", "")),
		// multiple condition groups: first match wins, order matters
		newCase("groups-first-match-wins",
			`[{"key":"k","active":true,"filters":{"groups":[{"properties":[{"key":"p","value":"a","operator":"exact","type":"person"}],"rollout_percentage":100},{"properties":[],"rollout_percentage":0}]}}]`,
			person("u", `{"p":"a"}`)),
		newCase("groups-second-matches",
			`[{"key":"k","active":true,"filters":{"groups":[{"properties":[{"key":"p","value":"a","operator":"exact","type":"person"}],"rollout_percentage":100},{"properties":[],"rollout_percentage":100}]}}]`,
			person("u", `{"p":"z"}`)),
		newCase("groups-all-miss",
			`[{"key":"k","active":true,"filters":{"groups":[{"properties":[{"key":"p","value":"a","operator":"exact","type":"person"}],"rollout_percentage":100}]}}]`,
			person("u", `{"p":"z"}`)),
		newCase("groups-multiple-properties-and",
			`[{"key":"k","active":true,"filters":{"groups":[{"properties":[{"key":"p","value":"a","operator":"exact","type":"person"},{"key":"q","value":10,"operator":"gt","type":"person"}],"rollout_percentage":100}]}}]`,
			person("u", `{"p":"a","q":11}`)),
		newCase("groups-multiple-properties-one-fails",
			`[{"key":"k","active":true,"filters":{"groups":[{"properties":[{"key":"p","value":"a","operator":"exact","type":"person"},{"key":"q","value":10,"operator":"gt","type":"person"}],"rollout_percentage":100}]}}]`,
			person("u", `{"p":"a","q":1}`)),
		// a failing person filter BEFORE a cohort filter short-circuits to a
		// plain miss rather than an inconclusive result
		newCase("groups-miss-before-cohort",
			`[{"key":"k","active":true,"filters":{"groups":[{"properties":[{"key":"p","value":"a","operator":"exact","type":"person"},{"key":"id","value":1,"type":"cohort"}],"rollout_percentage":100}]}}]`,
			person("u", `{"p":"z","id":1}`)),
		newCase("groups-cohort-before-miss",
			`[{"key":"k","active":true,"filters":{"groups":[{"properties":[{"key":"id","value":1,"type":"cohort"},{"key":"p","value":"a","operator":"exact","type":"person"}],"rollout_percentage":100}]}}]`,
			person("u", `{"p":"z","id":1}`)),
		newCase("groups-one-inconclusive-one-match",
			`[{"key":"k","active":true,"filters":{"groups":[{"properties":[{"key":"id","value":1,"type":"cohort"}]},{"properties":[],"rollout_percentage":100}]}}]`,
			person("u", "")),
	)

	// ── group-aggregated flags ───────────────────────────────────────────────
	groupFlag := `[{"key":"org-flag","active":true,"aggregation_group_type_index":0,"filters":{"groups":[{"properties":[{"key":"plan","value":"pro","operator":"exact","type":"group","group_type_index":0}],"rollout_percentage":100}]}}]`
	cs = append(cs,
		newCase("group-agg-hit", groupFlag, `{"distinct_id":"u","groups":{"0":{"key":"acme","properties":{"plan":"pro"}}}}`),
		newCase("group-agg-miss", groupFlag, `{"distinct_id":"u","groups":{"0":{"key":"acme","properties":{"plan":"free"}}}}`),
		newCase("group-agg-absent", groupFlag, `{"distinct_id":"u"}`),
		newCase("group-agg-empty-key", groupFlag, `{"distinct_id":"u","groups":{"0":{"key":"","properties":{"plan":"pro"}}}}`),
		newCase("group-agg-wrong-index", groupFlag, `{"distinct_id":"u","groups":{"1":{"key":"acme","properties":{"plan":"pro"}}}}`),
	)
	// The rollout hashes the GROUP key, not the distinct id.
	for i := 0; i < 20; i++ {
		cs = append(cs, newCase(fmt.Sprintf("group-agg-rollout-%d", i),
			`[{"key":"org-flag","active":true,"aggregation_group_type_index":0,"filters":{"groups":[{"properties":[],"rollout_percentage":50}]}}]`,
			fmt.Sprintf(`{"distinct_id":"same-user","groups":{"0":{"key":"org-%d","properties":{}}}}`, i)))
	}

	return cs
}

// TestWriteParityCases materializes the case table for the Rust oracle.
func TestWriteParityCases(t *testing.T) {
	if !*writeCases {
		t.Skip("pass -write-cases to regenerate testdata/cases.json")
	}
	cs := parityCases()
	b, err := json.MarshalIndent(cs, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("testdata", "cases.json"), append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d cases", len(cs))
}

// TestParityWithRustOracle is the gate: every case must evaluate to what the
// Rust implementation produced.
func TestParityWithRustOracle(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "oracle.json"))
	if err != nil {
		t.Skip("testdata/oracle.json absent — nothing to compare against")
	}
	var oracle map[string]string
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}

	cs := parityCases()
	if len(cs) != len(oracle) {
		t.Fatalf("case table has %d cases, oracle recorded %d — regenerate", len(cs), len(oracle))
	}

	var mismatches int
	for _, c := range cs {
		want, ok := oracle[c.Name]
		if !ok {
			t.Errorf("%s: no oracle answer recorded", c.Name)
			continue
		}
		got, err := EvaluateJSON(c.Defs, c.Ctx)
		if strings.HasPrefix(want, "ERR: ") {
			if err == nil {
				t.Errorf("%s: reference rejected the input (%s), this package accepted it", c.Name, want)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: evaluate: %v (reference produced %s)", c.Name, err, want)
			continue
		}
		gotC, err := canonical(got)
		if err != nil {
			t.Errorf("%s: canonicalize result: %v", c.Name, err)
			continue
		}
		wantC, err := canonical([]byte(want))
		if err != nil {
			t.Errorf("%s: canonicalize oracle: %v", c.Name, err)
			continue
		}
		if gotC != wantC {
			mismatches++
			if mismatches <= 25 {
				t.Errorf("%s:\n  defs %s\n  ctx  %s\n  rust %s\n  go   %s", c.Name, c.Defs, c.Ctx, wantC, gotC)
			}
		}
	}
	if mismatches > 25 {
		t.Errorf("... and %d further mismatches", mismatches-25)
	}
	t.Logf("%d cases compared against the Rust oracle", len(cs))
}

// canonical re-renders JSON with object keys sorted, preserving every scalar's
// exact source text. The reference stores its response in a hash map, so key
// order is randomized per process and cannot be compared — but the numbers and
// strings inside must still match character for character, which is where a
// float-formatting or escaping divergence would show up.
func canonical(b []byte) (string, error) {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	var sb strings.Builder
	writeCanonical(&sb, v)
	return sb.String(), nil
}

func writeCanonical(sb *strings.Builder, v any) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sb.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				sb.WriteByte(',')
			}
			writeEscaped(sb, k)
			sb.WriteByte(':')
			writeCanonical(sb, t[k])
		}
		sb.WriteByte('}')
	case []any:
		sb.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				sb.WriteByte(',')
			}
			writeCanonical(sb, e)
		}
		sb.WriteByte(']')
	case json.Number:
		sb.WriteString(string(t)) // verbatim: formatting differences must survive
	case string:
		writeEscaped(sb, t)
	case bool:
		if t {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case nil:
		sb.WriteString("null")
	}
}
