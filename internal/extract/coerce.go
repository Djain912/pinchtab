package extract

import (
	"strconv"
	"strings"

	"github.com/pinchtab/pinchtab/internal/bridge/observe"
)

// coerceNumber parses the FIRST numeric token of a display string into a
// float64, stripping a leading currency symbol/sign and the token's thousands
// separators while keeping sign and decimal. "$1,299.00" -> 1299, "−3.5 kg" ->
// -3.5 (Unicode minus honoured), "4.7 out of 5" -> 4.7, "2 of 3" -> 2, "call
// for price" -> not numeric. ok is false when no digit is present.
//
// It stops at the first rune after the token that is not a digit, '.', or ','
// so digits from a trailing word ("out of 5", "of 3") are never glued on — a
// coercion must leave a field missing, never hand back a wrong-typed value.
func coerceNumber(s string) (float64, bool) {
	var b strings.Builder
	sign := ""
	seenDigit := false
	seenDot := false
	done := false
	for _, r := range s {
		if done {
			break
		}
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			seenDigit = true
		case r == '.' && !seenDot:
			b.WriteRune('.')
			seenDot = true
		case r == ',' && seenDigit:
			// thousands separator inside the token: dropped
		case isMinus(r) && !seenDigit && sign == "":
			sign = "-"
		case r == '+' && !seenDigit && sign == "":
			sign = "+"
		default:
			// A non-numeric rune: before the first digit it is leading noise
			// (currency, spaces) and is skipped; after it, the token has ended.
			if seenDigit {
				done = true
			}
		}
	}
	if !seenDigit {
		return 0, false
	}
	f, err := strconv.ParseFloat(sign+b.String(), 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// isMinus reports whether r is an ASCII hyphen-minus or the Unicode minus sign
// (U+2212), which price and weight strings use interchangeably.
func isMinus(r rune) bool {
	return r == '-' || r == '−'
}

// coerceBool reads a boolean from a node: its accessibility checked state first,
// then yes/no/true/false words in the read value. ok is false when neither
// yields a decision (a mixed checkbox or an unrelated string).
func coerceBool(node observe.A11yNode, value string) (bool, bool) {
	switch node.Checked {
	case observe.CheckedTrue:
		return true, true
	case observe.CheckedFalse:
		return false, true
	case observe.CheckedMixed:
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "yes":
		return true, true
	case "false", "no":
		return false, true
	}
	return false, false
}
