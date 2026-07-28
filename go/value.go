package flags

// JSON values with serde_json semantics.
//
// The evaluator's operators compare properties by their *rendered* form
// ("is this value's string equal to that one's"), so the rendering has to agree
// with the reference implementation character for character or a flag can match
// on one side of the fleet and miss on the other. serde_json is that reference:
//
//   - numbers are re-normalized on parse — an integer literal stays an integer,
//     anything with a fraction or exponent becomes an f64 and is re-rendered by
//     ryu ("1.50" -> "1.5", "1e3" -> "1000.0", "1" -> "1")
//   - objects are BTreeMap-backed, so keys serialize in sorted order
//   - strings escape only ", \ and the C0 controls — no HTML escaping, no
//      /  (which is exactly where encoding/json would disagree)
//
// value is therefore parsed once, normalized, and rendered by this file rather
// than handed to encoding/json.

import (
	"bytes"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
)

type kind uint8

const (
	kNull kind = iota
	kBool
	kNum
	kStr
	kArr
	kObj
)

type member struct {
	key string
	val value
}

// value mirrors serde_json::Value. Numbers carry their normalized literal so
// rendering is a copy, never a re-format.
type value struct {
	k   kind
	b   bool
	num string
	s   string
	arr []value
	obj []member // sorted by key
}

func (v value) isString() bool { return v.k == kStr }
func (v value) isNumber() bool { return v.k == kNum }
func (v value) isBool() bool   { return v.k == kBool }
func (v value) isArray() bool  { return v.k == kArr }
func (v value) isNull() bool   { return v.k == kNull }

// str is serde_json's to_string_representation: a string yields its contents,
// everything else its compact JSON encoding.
func (v value) str() string {
	if v.k == kStr {
		return v.s
	}
	return v.json()
}

// json renders the value as serde_json would.
func (v value) json() string {
	var b strings.Builder
	v.writeJSON(&b)
	return b.String()
}

func (v value) writeJSON(b *strings.Builder) {
	switch v.k {
	case kNull:
		b.WriteString("null")
	case kBool:
		if v.b {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case kNum:
		b.WriteString(v.num)
	case kStr:
		writeEscaped(b, v.s)
	case kArr:
		b.WriteByte('[')
		for i, e := range v.arr {
			if i > 0 {
				b.WriteByte(',')
			}
			e.writeJSON(b)
		}
		b.WriteByte(']')
	case kObj:
		b.WriteByte('{')
		for i, m := range v.obj {
			if i > 0 {
				b.WriteByte(',')
			}
			writeEscaped(b, m.key)
			b.WriteByte(':')
			m.val.writeJSON(b)
		}
		b.WriteByte('}')
	}
}

// writeEscaped applies serde_json's string escaping: quote, backslash and the
// C0 controls only. Notably NOT encoding/json's HTML escaping of < > &, and not
// its  /  escaping — both would break parity.
func writeEscaped(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\b':
			b.WriteString(`\b`)
		case c == '\f':
			b.WriteString(`\f`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20:
			const hex = "0123456789abcdef"
			b.WriteString(`\u00`)
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xF])
		default:
			b.WriteByte(c) // UTF-8 passes through byte for byte
		}
	}
	b.WriteByte('"')
}

// asF64 is serde_json's Number::as_f64 for numeric values.
func (v value) asF64() (float64, bool) {
	if v.k != kNum {
		return 0, false
	}
	f, err := strconv.ParseFloat(v.num, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// ── parsing ─────────────────────────────────────────────────────────────────

// parseValue decodes raw JSON into a normalized value.
func parseValue(raw []byte) (value, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // keep the literal so normalization is ours, not encoding/json's
	var any any
	if err := dec.Decode(&any); err != nil {
		return value{}, err
	}
	return fromAny(any), nil
}

// fromAny normalizes a decoded (or caller-constructed) Go value. Go natives are
// accepted so library callers can pass plain map[string]any property bags.
func fromAny(a any) value {
	switch t := a.(type) {
	case nil:
		return value{k: kNull}
	case bool:
		return value{k: kBool, b: t}
	case string:
		return value{k: kStr, s: t}
	case json.Number:
		return value{k: kNum, num: normalizeNumber(string(t))}
	case json.RawMessage:
		v, err := parseValue(t)
		if err != nil {
			return value{k: kNull}
		}
		return v
	case float64:
		return value{k: kNum, num: formatFloat(t)}
	case float32:
		return value{k: kNum, num: formatFloat(float64(t))}
	case int:
		return value{k: kNum, num: strconv.FormatInt(int64(t), 10)}
	case int8:
		return value{k: kNum, num: strconv.FormatInt(int64(t), 10)}
	case int16:
		return value{k: kNum, num: strconv.FormatInt(int64(t), 10)}
	case int32:
		return value{k: kNum, num: strconv.FormatInt(int64(t), 10)}
	case int64:
		return value{k: kNum, num: strconv.FormatInt(t, 10)}
	case uint:
		return value{k: kNum, num: strconv.FormatUint(uint64(t), 10)}
	case uint8:
		return value{k: kNum, num: strconv.FormatUint(uint64(t), 10)}
	case uint16:
		return value{k: kNum, num: strconv.FormatUint(uint64(t), 10)}
	case uint32:
		return value{k: kNum, num: strconv.FormatUint(uint64(t), 10)}
	case uint64:
		return value{k: kNum, num: strconv.FormatUint(t, 10)}
	case []any:
		out := value{k: kArr, arr: make([]value, len(t))}
		for i, e := range t {
			out.arr[i] = fromAny(e)
		}
		return out
	case map[string]any:
		out := value{k: kObj, obj: make([]member, 0, len(t))}
		for k, e := range t {
			out.obj = append(out.obj, member{key: k, val: fromAny(e)})
		}
		sort.Slice(out.obj, func(i, j int) bool { return out.obj[i].key < out.obj[j].key })
		return out
	default:
		// Anything exotic round-trips through encoding/json rather than guessing.
		b, err := json.Marshal(t)
		if err != nil {
			return value{k: kNull}
		}
		v, err := parseValue(b)
		if err != nil {
			return value{k: kNull}
		}
		return v
	}
}

// normalizeNumber applies serde_json's parse rule: a literal with no fraction
// and no exponent is an integer (u64/i64, falling back to f64 on overflow);
// everything else is an f64 re-rendered by ryu.
func normalizeNumber(lit string) string {
	if !strings.ContainsAny(lit, ".eE") {
		if strings.HasPrefix(lit, "-") {
			if n, err := strconv.ParseInt(lit, 10, 64); err == nil {
				return strconv.FormatInt(n, 10)
			}
		} else if n, err := strconv.ParseUint(lit, 10, 64); err == nil {
			return strconv.FormatUint(n, 10)
		}
	}
	f, err := strconv.ParseFloat(lit, 64)
	if err != nil {
		return lit
	}
	return formatFloat(f)
}

// formatFloat reproduces ryu's "pretty" f64 rendering, which is what serde_json
// emits. Given the shortest round-tripping decimal mantissa d (length digits)
// and exponent k where value == d * 10^k, ryu sets kk = length + k and picks:
//
//	0 <= k && kk <= 16   digits, zero padding, ".0"   -> 100.0
//	0 <  kk && kk <= 16  point inside the digits      -> 1.5
//	-5 < kk && kk <= 0   leading "0.000"              -> 0.001
//	length == 1          single digit + exponent      -> 1e100
//	otherwise            d.ddd + exponent             -> 1.5e-8
func formatFloat(f float64) string {
	switch {
	case math.IsNaN(f) || math.IsInf(f, 0):
		return "null" // serde_json cannot hold non-finite numbers
	case f == 0:
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}

	sign := ""
	if f < 0 {
		sign = "-"
		f = -f
	}

	// Go's 'e' format yields the same shortest digits ryu computes.
	sci := strconv.FormatFloat(f, 'e', -1, 64) // d[.ddd]e±dd
	mant, expPart, _ := strings.Cut(sci, "e")
	digits := strings.Replace(mant, ".", "", 1)
	sciExp, err := strconv.Atoi(expPart)
	if err != nil {
		return sci
	}
	length := len(digits)
	kk := sciExp + 1 // 10^(kk-1) <= f < 10^kk
	k := kk - length

	var b strings.Builder
	b.WriteString(sign)
	switch {
	case k >= 0 && kk <= 16:
		b.WriteString(digits)
		b.WriteString(strings.Repeat("0", k))
		b.WriteString(".0")
	case kk > 0 && kk <= 16:
		b.WriteString(digits[:kk])
		b.WriteByte('.')
		b.WriteString(digits[kk:])
	case kk > -5 && kk <= 0:
		b.WriteString("0.")
		b.WriteString(strings.Repeat("0", -kk))
		b.WriteString(digits)
	case length == 1:
		b.WriteString(digits)
		writeExponent(&b, kk-1)
	default:
		b.WriteString(digits[:1])
		b.WriteByte('.')
		b.WriteString(digits[1:])
		writeExponent(&b, kk-1)
	}
	return b.String()
}

// writeExponent emits the exponent with an explicit sign on both sides and no
// zero padding: 1e+100, 1.5e-8.
func writeExponent(b *strings.Builder, exp int) {
	b.WriteByte('e')
	if exp >= 0 {
		b.WriteByte('+')
	}
	b.WriteString(strconv.Itoa(exp))
}
