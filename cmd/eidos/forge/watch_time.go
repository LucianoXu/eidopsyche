package forge

import "time"

// unixToISOZ formats a unix-epoch second as "YYYY-MM-DD HH:MM:SS" in UTC.
// Used by `forge watch --list` so the column width is stable.
func unixToISOZ(sec int64) string {
	if sec == 0 {
		return "-"
	}
	return time.Unix(sec, 0).UTC().Format("2006-01-02 15:04:05")
}
