package app

import "testing"

func TestFormatBytes(t *testing.T) {
	for n, want := range map[int64]string{
		0:                 "0 B",
		512:               "512 B",
		1023:              "1023 B",
		1024:              "1 KiB",
		1536:              "1.5 KiB",
		12_939_428:        "12.34 MiB",
		5 * 1024 * 1024:   "5 MiB",
		3_221_225_472:     "3 GiB",
		1_649_267_441_664: "1.5 TiB",
		1 << 60:           "1048576 TiB",
	} {
		if got := FormatBytes(n); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
