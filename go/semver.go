package flags

// Semantic versions, matching the Rust `semver` crate the reference evaluator
// uses — which is Cargo's dialect, not the bare SemVer spec:
//
//   - parsing is strict: three numeric components, no leading zeros, no
//     surrounding whitespace (the evaluator strips a leading "v" and trims first)
//   - ORDERING ignores nothing: build metadata participates, so 1.2.3+build.1
//     sorts below 1.2.3+build.2 even though the spec says they have equal
//     precedence
//   - a bare requirement means caret: "1.2.3" is "^1.2.3"
//   - a prerelease version only satisfies a requirement when some comparator
//     carries a prerelease on the same major.minor.patch, so ^1.2.3 rejects
//     1.3.0-alpha

import (
	"strconv"
	"strings"
)

type semver struct {
	major, minor, patch uint64
	pre                 []string
	build               []string
}

// normalizeVersion strips a leading "v" and then trims. The order matters and
// is the reference's: " v1.2.3 " does not normalize, because the "v" is not at
// position zero when the prefix is tested.
func normalizeVersion(s string) string {
	return strings.TrimSpace(strings.TrimPrefix(s, "v"))
}

func toSemver(v value) (semver, bool) {
	return parseVersion(normalizeVersion(v.str()))
}

func parseVersion(s string) (semver, bool) {
	var out semver
	rest := s
	if i := strings.IndexByte(rest, '+'); i >= 0 {
		b, ok := parseIdents(rest[i+1:], true)
		if !ok {
			return out, false
		}
		out.build = b
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '-'); i >= 0 {
		p, ok := parseIdents(rest[i+1:], false)
		if !ok {
			return out, false
		}
		out.pre = p
		rest = rest[:i]
	}
	parts := strings.Split(rest, ".")
	if len(parts) != 3 {
		return out, false
	}
	var ok bool
	if out.major, ok = parseNumeric(parts[0]); !ok {
		return out, false
	}
	if out.minor, ok = parseNumeric(parts[1]); !ok {
		return out, false
	}
	if out.patch, ok = parseNumeric(parts[2]); !ok {
		return out, false
	}
	return out, true
}

// parseNumeric accepts digits with no leading zero (bare "0" excepted).
func parseNumeric(s string) (uint64, bool) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// parseIdents splits dot-separated identifiers. Prerelease numeric identifiers
// may not carry leading zeros; build metadata may.
func parseIdents(s string, isBuild bool) ([]string, bool) {
	if s == "" {
		return nil, false
	}
	parts := strings.Split(s, ".")
	for _, p := range parts {
		if p == "" {
			return nil, false
		}
		for i := 0; i < len(p); i++ {
			c := p[i]
			alnum := c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-'
			if !alnum {
				return nil, false
			}
		}
		if !isBuild && isNumericIdent(p) && len(p) > 1 && p[0] == '0' {
			return nil, false
		}
	}
	return parts, true
}

// identValue reads a numeric identifier, tolerating the leading zeros build
// metadata is allowed to carry.
func identValue(s string) uint64 {
	n, err := strconv.ParseUint(strings.TrimLeft(s, "0"), 10, 64)
	if err != nil {
		return 0 // all zeros, or wider than u64
	}
	return n
}

func isNumericIdent(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

// equal is structural equality, build metadata included.
func (v semver) equal(o semver) bool {
	return v.major == o.major && v.minor == o.minor && v.patch == o.patch &&
		identsEqual(v.pre, o.pre) && identsEqual(v.build, o.build)
}

func identsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (v semver) compare(o semver) int {
	if c := cmpUint(v.major, o.major); c != 0 {
		return c
	}
	if c := cmpUint(v.minor, o.minor); c != 0 {
		return c
	}
	if c := cmpUint(v.patch, o.patch); c != 0 {
		return c
	}
	if c := cmpPre(v.pre, o.pre); c != 0 {
		return c
	}
	return cmpIdents(v.build, o.build)
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpPre orders prereleases, where "absent" ranks above any prerelease.
func cmpPre(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	}
	return cmpIdents(a, b)
}

// cmpIdents compares identifier lists: numeric before alphanumeric, numerics by
// value, alphanumerics bytewise, and a shorter list before its own prefix.
func cmpIdents(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		an, bn := isNumericIdent(a[i]), isNumericIdent(b[i])
		switch {
		case an && bn:
			if c := cmpUint(identValue(a[i]), identValue(b[i])); c != 0 {
				return c
			}
			if c := cmpUint(uint64(len(a[i])), uint64(len(b[i]))); c != 0 {
				return c // "090" ranks above "90" once the values tie
			}
		case an:
			return -1
		case bn:
			return 1
		default:
			if c := strings.Compare(a[i], b[i]); c != 0 {
				return c
			}
		}
	}
	return cmpUint(uint64(len(a)), uint64(len(b)))
}

// ── requirements ────────────────────────────────────────────────────────────

type op uint8

const (
	opCaret op = iota
	opTilde
	opExactReq
	opWildcard
)

type comparator struct {
	o            op
	major        uint64
	minor, patch *uint64
	pre          []string
}

type versionReq struct{ comparators []comparator }

// parseVersionReq reads the single-comparator forms the evaluator builds:
// "^X.Y.Z", "~X.Y.Z", "X.Y.x" and "x".
func parseVersionReq(s string) (versionReq, bool) {
	s = strings.TrimSpace(s)
	c := comparator{o: opCaret}
	explicit := false
	switch {
	case strings.HasPrefix(s, "^"):
		c.o, s, explicit = opCaret, s[1:], true
	case strings.HasPrefix(s, "~"):
		c.o, s, explicit = opTilde, s[1:], true
	case strings.HasPrefix(s, "="):
		c.o, s, explicit = opExactReq, s[1:], true
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return versionReq{}, false
	}

	if i := strings.IndexByte(s, '+'); i >= 0 {
		if _, ok := parseIdents(s[i+1:], true); !ok {
			return versionReq{}, false
		}
		s = s[:i]
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		p, ok := parseIdents(s[i+1:], false)
		if !ok {
			return versionReq{}, false
		}
		c.pre = p
		s = s[:i]
	}

	parts := strings.Split(s, ".")
	if len(parts) > 3 {
		return versionReq{}, false
	}
	// A wildcard major means "any version at all" — no comparators to satisfy.
	if isWildcard(parts[0]) {
		return versionReq{}, len(parts) == 1 || allWildcard(parts[1:])
	}
	var ok bool
	if c.major, ok = parseNumeric(parts[0]); !ok {
		return versionReq{}, false
	}
	for i, p := range parts[1:] {
		if isWildcard(p) {
			if !explicit {
				c.o = opWildcard
			}
			if !allWildcard(parts[i+2:]) {
				return versionReq{}, false
			}
			break
		}
		n, ok := parseNumeric(p)
		if !ok {
			return versionReq{}, false
		}
		if i == 0 {
			c.minor = &n
		} else {
			c.patch = &n
		}
	}
	if len(parts) == 1 && !explicit {
		c.o = opWildcard // a bare major behaves as "X.*"
	}
	return versionReq{comparators: []comparator{c}}, true
}

func isWildcard(s string) bool { return s == "x" || s == "X" || s == "*" }

func allWildcard(parts []string) bool {
	for _, p := range parts {
		if !isWildcard(p) {
			return false
		}
	}
	return true
}

// matches applies every comparator, then the prerelease rule: a prerelease
// version needs a comparator that carries a prerelease on the same triple.
func (r versionReq) matches(v semver) bool {
	for _, c := range r.comparators {
		if !c.matches(v) {
			return false
		}
	}
	if len(v.pre) == 0 {
		return true
	}
	for _, c := range r.comparators {
		if c.major == v.major && c.minor != nil && *c.minor == v.minor &&
			c.patch != nil && *c.patch == v.patch && len(c.pre) > 0 {
			return true
		}
	}
	return false
}

func (c comparator) matches(v semver) bool {
	switch c.o {
	case opTilde:
		return c.matchesTilde(v)
	case opCaret:
		return c.matchesCaret(v)
	default: // exact and wildcard share the same test
		return c.matchesExact(v)
	}
}

func (c comparator) matchesExact(v semver) bool {
	if v.major != c.major {
		return false
	}
	if c.minor != nil && v.minor != *c.minor {
		return false
	}
	if c.patch != nil && v.patch != *c.patch {
		return false
	}
	return cmpPre(v.pre, c.pre) == 0
}

func (c comparator) matchesTilde(v semver) bool {
	if v.major != c.major {
		return false
	}
	if c.minor != nil && v.minor != *c.minor {
		return false
	}
	if c.patch != nil && v.patch != *c.patch {
		return v.patch > *c.patch
	}
	return cmpPre(v.pre, c.pre) >= 0
}

func (c comparator) matchesCaret(v semver) bool {
	if v.major != c.major {
		return false
	}
	if c.minor == nil {
		return true
	}
	minor := *c.minor
	if c.patch == nil {
		if c.major > 0 {
			return v.minor >= minor
		}
		return v.minor == minor
	}
	patch := *c.patch
	switch {
	case c.major > 0:
		if v.minor != minor {
			return v.minor > minor
		}
		if v.patch != patch {
			return v.patch > patch
		}
	case minor > 0:
		if v.minor != minor {
			return false
		}
		if v.patch != patch {
			return v.patch > patch
		}
	default:
		if v.minor != minor || v.patch != patch {
			return false
		}
	}
	return cmpPre(v.pre, c.pre) >= 0
}
