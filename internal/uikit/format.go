package uikit

import (
	"math"
	"strconv"
	"strings"
)

// FormatCompactCount renders a count with compact human units, the way the
// product shows token counts and other large numbers (DSH-style): values under
// one thousand stay exact ("460"), and larger values use one decimal at most
// with a K/M/B suffix, trimmed when the decimal is zero:
//
//	1_234         → "1.2K"
//	173_635       → "173.6K"
//	8_400_000     → "8.4M"
//	1_200_000_000 → "1.2B"
//
// Rounding carries into the next unit ("999_950" → "1M") instead of rendering
// "1000K". Zero and negative values are safe: negatives keep their sign
// ("-1.2K") and zero stays "0".
func FormatCompactCount(count int) string {
	if count > -1000 && count < 1000 {
		return strconv.Itoa(count)
	}
	units := [...]string{"K", "M", "B"}
	negative := count < 0
	value := float64(count)
	if negative {
		value = -value
	}
	unit := 0
	value /= 1000
	for value >= 1000 && unit < len(units)-1 {
		value /= 1000
		unit++
	}
	// One decimal at most; a rounded 1000 is promoted to the next unit so a
	// count near a unit boundary never renders as "1000K".
	rounded := math.Round(value*10) / 10
	if rounded >= 1000 && unit < len(units)-1 {
		rounded /= 1000
		unit++
	}
	text := strconv.FormatFloat(rounded, 'f', 1, 64)
	text = strings.TrimSuffix(text, ".0")
	if negative {
		text = "-" + text
	}
	return text + units[unit]
}
