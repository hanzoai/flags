package flags

// The rollout hash. This is the single most parity-critical function in the
// package: it decides which identities fall inside a percentage rollout, so any
// drift here silently splits a cohort differently than the rest of the fleet
// (and than Hanzo Insights, which is PostHog-compatible).
//
// sha1("{prefix}{identifier}{salt}"), first 8 bytes big-endian, shifted right 4
// bits (= the first 15 hex characters), scaled into [0, 1).

import (
	"crypto/sha1"
	"encoding/binary"
)

// longScale is 15 hex digits of headroom — the divisor PostHog picked.
const longScale = float64(0xfffffffffffffff)

// hashOf takes prefix "{flag_key}." (the trailing dot is part of the wire
// contract) and salt "" for rollout gating or "variant" for variant selection.
func hashOf(prefix, identifier, salt string) float64 {
	sum := sha1.Sum([]byte(prefix + identifier + salt))
	return float64(binary.BigEndian.Uint64(sum[:8])>>4) / longScale
}
