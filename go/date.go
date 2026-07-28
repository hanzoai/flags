package flags

// Date parsing for the is_date_* operators.
//
// The reference tries, in order: a relative offset ("-3d"), then the
// `dateparser` crate, then a bare ISO datetime read as UTC. Two of its
// behaviours are load-bearing and surprising, so they are reproduced rather
// than tidied up:
//
//   - a datetime with no offset is read in the process's LOCAL zone, so the
//     same definition can decide differently on two hosts
//   - a date-only value ("2021-05-06") splices in the current wall-clock
//     time-of-day, so it is not even stable across two calls in one process
//
// Only dateparser's deterministic families are reproduced (unix timestamps,
// RFC 3339, "Y-m-d H:M[:S]", and bare dates). Its locale-ish families —
// RFC 2822, month names, slash and dot orders, MySQL log and Chinese formats —
// are not: every one of them is date-only, hence non-deterministic by
// construction, and none is reachable from a flag definition anyone can rely on.

import (
	"math"
	"regexp"
	"strconv"
	"time"
)

var (
	reRelative = regexp.MustCompile(`^-?([0-9]+)([hdwmy])$`)
	reUnix     = regexp.MustCompile(`^[0-9]{10,19}$`)
	reYMDHMS   = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}\s+[0-9]{2}:[0-9]{2}(:[0-9]{2})?(\.[0-9]{1,9})?\s*(am|pm|AM|PM)?$`)
	reYMD      = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)
)

// parseRelativeDate reads "-3d" / "3d" style offsets back from now. Hours, days
// and weeks are fixed durations; months and years step calendar-wise, clamping
// the day at every step (so three months back from 31 March is 28 December,
// not 31 December).
func parseRelativeDate(s string, now time.Time) (time.Time, bool) {
	m := reRelative.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n >= 10000 { // guard against overflow, as the reference does
		return time.Time{}, false
	}
	switch m[2] {
	case "h":
		return now.Add(-time.Duration(n) * time.Hour), true
	case "d":
		return now.AddDate(0, 0, -n), true
	case "w":
		return now.AddDate(0, 0, -7*n), true
	case "m":
		t := now
		for i := 0; i < n; i++ {
			y, mo := t.Year(), int(t.Month())-1
			if mo == 0 {
				y, mo = y-1, 12
			}
			day := min(t.Day(), daysIn(y, mo))
			t = time.Date(y, time.Month(mo), day, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
		}
		return t, true
	default: // "y"
		t := now
		for i := 0; i < n; i++ {
			y := t.Year() - 1
			day := t.Day()
			if t.Month() == time.February && day == 29 && !leap(y) {
				day = 28
			}
			t = time.Date(y, t.Month(), day, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
		}
		return t, true
	}
}

func leap(y int) bool { return y%4 == 0 && (y%100 != 0 || y%400 == 0) }

func daysIn(y, m int) int {
	switch m {
	case 2:
		if leap(y) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	}
	return 31
}

// parseDateString is the reference's chain: relative offset, then dateparser,
// then a bare ISO datetime interpreted as UTC.
func parseDateString(s string) (time.Time, bool) {
	if t, ok := parseRelativeDate(s, time.Now().UTC()); ok {
		return t, true
	}
	if t, ok := dateparse(s); ok {
		return t, true
	}
	for _, layout := range []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// dateparse reproduces the deterministic part of the dateparser crate.
func dateparse(s string) (time.Time, bool) {
	// Unix timestamps, but only at the three lengths the crate accepts.
	if reUnix.MatchString(s) {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		switch len(s) {
		case 10:
			return time.Unix(n, 0).UTC(), true
		case 13:
			return time.UnixMilli(n).UTC(), true
		case 19:
			return time.Unix(0, n).UTC(), true
		}
		return time.Time{}, false
	}
	// The remaining families all begin with a year-month.
	if !isYearMonthPrefix(s) {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), true
	}
	if reYMDHMS.MatchString(s) {
		for _, layout := range []string{
			"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02 15:04:05.999999999",
			"2006-01-02 03:04:05 pm", "2006-01-02 03:04 pm",
		} {
			if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
				return t.UTC(), true
			}
		}
	}
	if reYMD.MatchString(s) {
		// Date-only: the crate fills the time from the current local clock.
		now := time.Now().In(time.Local)
		d, err := time.ParseInLocation("2006-01-02", s, time.Local)
		if err != nil {
			return time.Time{}, false
		}
		return time.Date(d.Year(), d.Month(), d.Day(), now.Hour(), now.Minute(), now.Second(), now.Nanosecond(), time.Local).UTC(), true
	}
	return time.Time{}, false
}

func isYearMonthPrefix(s string) bool {
	if len(s) < 7 {
		return false
	}
	for i, c := range []byte(s[:7]) {
		if i == 4 {
			if c != '-' {
				return false
			}
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// propertyDate reads the date a property carries. A numeric string is a unix
// timestamp BEFORE it is anything else — which is why a 13-digit millisecond
// stamp means one instant as a property value and another as a filter value.
func propertyDate(v value) (time.Time, bool) {
	switch v.k {
	case kStr:
		if f, err := strconv.ParseFloat(v.s, 64); err == nil {
			return floatTimestamp(f)
		}
		return parseDateString(v.s)
	case kNum:
		f, ok := v.asF64()
		if !ok {
			return time.Time{}, false
		}
		return floatTimestamp(f)
	}
	return time.Time{}, false
}

// floatTimestamp splits seconds-since-epoch into whole seconds and nanoseconds.
// The fractional part is taken with the sign of the value and then cast to an
// unsigned count, which saturates at zero — so a negative fraction contributes
// nothing rather than moving the instant backwards.
func floatTimestamp(f float64) (time.Time, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return time.Time{}, false
	}
	secs := math.Floor(f)
	if secs < math.MinInt64 || secs > math.MaxInt64 {
		return time.Time{}, false
	}
	nanos := math.Round(math.Mod(f, 1) * 1e9)
	switch {
	case nanos < 0:
		nanos = 0
	case nanos > math.MaxUint32:
		nanos = math.MaxUint32
	}
	if nanos >= 2e9 { // chrono rejects a nanosecond count this large
		return time.Time{}, false
	}
	t := time.Unix(int64(secs), int64(nanos)).UTC()
	if t.Year() < 1 || t.Year() > 9999 {
		return time.Time{}, false
	}
	return t, true
}
