package app

import (
	"strconv"
	"strings"
)

var byteUnits = []string{"B", "KiB", "MiB", "GiB", "TiB"}

// FormatBytes formats a byte count like Netch: binary units with at most two
// decimals, for example "512 B", "1.5 KiB" and "12.34 MiB".
func FormatBytes(n int64) string {
	v, i := float64(n), 0
	for v >= 1024 && i < len(byteUnits)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return strconv.FormatInt(n, 10) + " B"
	}
	s := strings.TrimRight(strings.TrimRight(strconv.FormatFloat(v, 'f', 2, 64), "0"), ".")
	return s + " " + byteUnits[i]
}
