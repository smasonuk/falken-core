package pluginsdk

import (
	"math"
)

// NumberToUint32 converts a decoded JSON number into a uint32 when possible.
func NumberToUint32(v any) (uint32, bool) {
	n, ok := v.(float64)
	if !ok || n < 0 || n > math.MaxUint32 {
		return 0, false
	}

	return uint32(n), true
}
