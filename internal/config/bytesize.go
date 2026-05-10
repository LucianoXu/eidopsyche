package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseByteSize accepts a plain byte count ("10485760") or a number with
// a K/M/G suffix ("100MB", "5G", "512KiB"). Suffixes are case-insensitive
// and the trailing "B"/"iB" is optional. Returns the value in bytes.
//
// Powers-of-1024 are used for K/M/G, matching the spec's "100MB" example
// (≈ what most operators expect for disk budgets).
func ParseByteSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty byte size")
	}
	upper := strings.ToUpper(s)
	upper = strings.TrimSuffix(upper, "IB")
	upper = strings.TrimSuffix(upper, "B")

	mult := int64(1)
	switch {
	case strings.HasSuffix(upper, "G"):
		mult = 1024 * 1024 * 1024
		upper = strings.TrimSuffix(upper, "G")
	case strings.HasSuffix(upper, "M"):
		mult = 1024 * 1024
		upper = strings.TrimSuffix(upper, "M")
	case strings.HasSuffix(upper, "K"):
		mult = 1024
		upper = strings.TrimSuffix(upper, "K")
	}
	upper = strings.TrimSpace(upper)
	n, err := strconv.ParseInt(upper, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("byte size %q: %w", s, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("byte size %q must be non-negative", s)
	}
	return n * mult, nil
}
