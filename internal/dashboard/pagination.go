package dashboard

import (
	"encoding/base64"
	"strconv"
	"strings"
)

// encodeCursor packs a unix-second timestamp into a URL-safe cursor.
func encodeCursor(unixSec int64) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(unixSec, 10)))
}

// decodeCursor returns 0 for the empty string and on any decode error
// (treats malformed cursor as "start from latest"; the caller then sees
// an empty page if there are no rows).
func decodeCursor(s string) int64 {
	if s == "" {
		return 0
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return 0
	}
	return v
}
