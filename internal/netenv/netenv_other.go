//go:build !windows

package netenv

import "errors"

// Collect is only implemented on Windows.
func Collect() (Snapshot, error) {
	return Snapshot{}, errors.New("netenv: not supported on this system")
}
