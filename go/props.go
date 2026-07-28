package flags

// Property matching — the operator set a condition group's filters apply.
//
// The reference implementation calls its matcher in "partial properties" mode
// only: a property the context does not carry is a non-match rather than an
// error, and every internal error (a bad number, an unparseable version, a
// cohort operator) also collapses to a non-match. Both collapse to false here,
// which is why this returns a plain bool — fail-safe, never fail-wrong.
//
// Because a missing key already returned, every operator below sees a value
// that is definitely present; the reference's "value absent" branches are dead
// in this mode and are not reproduced.

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// matchProperty reports whether one property filter is satisfied by a property bag.
func matchProperty(f PropertyFilter, props map[string]value) bool {
	mv, present := props[f.Key]
	if !present {
		return false
	}

	op := OpExact
	if f.Operator != nil {
		op = *f.Operator
	}
	switch op {
	case OpIsSet:
		return true
	case OpIsNotSet:
		return false
	}

	if f.Value == nil {
		return false // a value-requiring operator with no value never matches
	}
	fv, err := parseValue(f.Value)
	if err != nil {
		return false
	}

	switch op {
	case OpExact:
		return exactMatch(fv, mv)
	case OpIsNot:
		return !exactMatch(fv, mv)

	case OpIcontains, OpNotIcontains:
		// ASCII-only folding here, deliberately: the reference uses
		// to_ascii_lowercase for containment and full Unicode lowercasing for
		// equality, and that asymmetry is observable.
		in := strings.Contains(asciiLower(mv.str()), asciiLower(fv.str()))
		return in == (op == OpIcontains)

	case OpRegex, OpNotRegex:
		re, err := regexp.Compile(fv.str())
		if err != nil {
			return false // an unparseable pattern is false for BOTH polarities
		}
		return re.MatchString(mv.str()) == (op == OpRegex)

	case OpGt, OpGte, OpLt, OpLte:
		lhs, ok := toFloat(mv)
		if !ok {
			return false
		}
		rhs, ok := toFloat(fv)
		if !ok {
			return false
		}
		switch op {
		case OpGt:
			return lhs > rhs
		case OpGte:
			return lhs >= rhs
		case OpLt:
			return lhs < rhs
		default:
			return lhs <= rhs
		}

	case OpSemverGt, OpSemverGte, OpSemverLt, OpSemverLte, OpSemverEq, OpSemverNeq:
		lhs, ok := toSemver(mv)
		if !ok {
			return false
		}
		rhs, ok := toSemver(fv)
		if !ok {
			return false
		}
		switch op {
		case OpSemverGt:
			return lhs.compare(rhs) > 0
		case OpSemverGte:
			return lhs.compare(rhs) >= 0
		case OpSemverLt:
			return lhs.compare(rhs) < 0
		case OpSemverLte:
			return lhs.compare(rhs) <= 0
		case OpSemverEq:
			// Equality is structural, so it DOES distinguish build metadata
			// even though ordering ignores it. That asymmetry is the reference's.
			return lhs.equal(rhs)
		default:
			return !lhs.equal(rhs)
		}

	case OpSemverTilde, OpSemverCaret, OpSemverWildcard:
		v, ok := toSemver(mv)
		if !ok {
			return false
		}
		spec := normalizeVersion(fv.str())
		switch op {
		case OpSemverTilde:
			spec = "~" + spec
		case OpSemverCaret:
			spec = "^" + spec
		default:
			spec = strings.ReplaceAll(spec, "*", "x")
		}
		req, ok := parseVersionReq(spec)
		if !ok {
			return false
		}
		return req.matches(v)

	case OpIsDateExact, OpIsDateAfter, OpIsDateBefore:
		got, ok := propertyDate(mv)
		if !ok {
			return false
		}
		if !fv.isString() {
			return false
		}
		want, ok := parseDateString(fv.s)
		if !ok {
			return false
		}
		switch op {
		case OpIsDateBefore:
			return got.Before(want)
		case OpIsDateAfter:
			return got.After(want)
		default:
			return got.Equal(want)
		}
	}

	// in / not_in belong to cohort matching and flag_evaluates_to to flag
	// dependency matching; neither is answerable here.
	return false
}

// exactMatch compares a filter value against a property value. Booleans (and
// their "true"/"false" string spellings) compare by truthiness; an array filter
// value is a membership test; everything else is a case-insensitive string
// comparison of the rendered forms.
func exactMatch(fv, mv value) bool {
	if isTruthyOrFalsy(fv) {
		return isTruthy(fv) == isTruthy(mv)
	}
	if fv.isArray() {
		target := rustLower(mv.str())
		for _, e := range fv.arr {
			if rustLower(e.str()) == target {
				return true
			}
		}
		return false
	}
	return rustLower(fv.str()) == rustLower(mv.str())
}

func isTruthyOrFalsy(v value) bool {
	switch v.k {
	case kBool:
		return true
	case kStr:
		s := rustLower(v.s)
		return s == "true" || s == "false"
	case kArr:
		for _, e := range v.arr {
			if !isTruthyOrFalsy(e) {
				return false
			}
		}
		return true // vacuously true for an empty array, as in the reference
	}
	return false
}

func isTruthy(v value) bool {
	switch v.k {
	case kBool:
		return v.b
	case kStr:
		return rustLower(v.s) == "true"
	case kArr:
		for _, e := range v.arr {
			if !isTruthy(e) {
				return false
			}
		}
		return true
	}
	return false
}

// toFloat is the reference's to_f64_representation: numbers as themselves,
// everything else parsed from its rendered form.
func toFloat(v value) (float64, bool) {
	if f, ok := v.asF64(); ok {
		return f, true
	}
	return parseFloatStrict(v.str())
}

// parseFloatStrict accepts exactly what Rust's f64::from_str accepts — decimal
// digits with an optional fraction and exponent, or inf/infinity/nan. Go's
// ParseFloat additionally takes hex floats and underscores; those are rejected
// here so a value like "0x1p2" is a non-match on both sides.
func parseFloatStrict(s string) (float64, bool) {
	body := strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	if body == "" {
		return 0, false
	}
	switch asciiLower(body) {
	case "inf", "infinity", "nan":
		f, err := strconv.ParseFloat(s, 64)
		return f, err == nil
	}
	for i := 0; i < len(body); i++ {
		switch c := body[i]; {
		case c >= '0' && c <= '9', c == '.', c == 'e', c == 'E', c == '+', c == '-':
		default:
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil
}

// ── case folding ────────────────────────────────────────────────────────────

func asciiLower(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

// rustLower reproduces Rust's str::to_lowercase, which applies Unicode's full
// lowercase mapping rather than Go's simple one. Two characters differ:
// U+0130 expands to two code points, and a capital sigma folds to the final
// form at the end of a word.
func rustLower(s string) string {
	if isASCII(s) {
		return asciiLower(s)
	}
	rs := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range rs {
		switch r {
		case 'İ': // LATIN CAPITAL LETTER I WITH DOT ABOVE
			b.WriteString("i̇")
		case 'Σ': // GREEK CAPITAL LETTER SIGMA
			if finalSigma(rs, i) {
				b.WriteRune('ς')
			} else {
				b.WriteRune('σ')
			}
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8Self {
			return false
		}
	}
	return true
}

const utf8Self = 0x80

// finalSigma is Unicode's Final_Sigma condition: the sigma is preceded by a
// cased character and not followed by one, ignoring case-ignorable characters
// on both sides.
func finalSigma(rs []rune, i int) bool {
	before := false
	for j := i - 1; j >= 0; j-- {
		if caseIgnorable(rs[j]) {
			continue
		}
		before = cased(rs[j])
		break
	}
	if !before {
		return false
	}
	for j := i + 1; j < len(rs); j++ {
		if caseIgnorable(rs[j]) {
			continue
		}
		return !cased(rs[j])
	}
	return true
}

func cased(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r)
}

func caseIgnorable(r rune) bool {
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}
