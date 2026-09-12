package extract

import (
	"strconv"
	"strings"

	"github.com/pinchtab/pinchtab/internal/bridge/observe"
)

// coerceNumber parses a display string into a float64, stripping currency
// symbols, thousands separators, and trailing units while keeping sign and
// decimal. "$1,299.00" -> 1299, "−3.5 kg" -> -3.5 (Unicode minus honoured),
// "call for price" -> not numeric. ok is false when no digit is present.
func coerceNumber(s string) (float64, bool) {
	var b strings.Builder
	sign := ""
	seenDigit := false
	seenDot := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			seenDigit = true
		case r == '.' && !seenDot:
			b.WriteRune('.')
			seenDot = true
		case isMinus(r) && !seenDigit && sign == "":
			sign = "-"
		case r == '+' && !seenDigit && sign == "":
			sign = "+"
		default:
			// currency, letters, spaces, thousands separators: dropped
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
