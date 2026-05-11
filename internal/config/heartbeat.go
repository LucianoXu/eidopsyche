package config

import (
	"fmt"
	"strconv"
	"time"
)

// DefaultHeartbeatInterval is the cadence used when [heartbeat] interval
// is unset. 2h is a balance between "notice the day going by" and "do
// not burn API budget on idle wakes". Operators who want a different
// cadence set [heartbeat] interval explicitly.
const DefaultHeartbeatInterval = 2 * time.Hour

// DefaultDreamMinInterval is used when [mindform] dream_min_interval is unset.
const DefaultDreamMinInterval = 12 * time.Hour

// supportedMinuteSteps is {N : 60 mod N == 0, N <= 30}, sorted.
var supportedMinuteSteps = []int{1, 2, 3, 4, 5, 6, 10, 12, 15, 20, 30}

// supportedHourSteps is {M : 24 mod M == 0, 1 <= M <= 24}, sorted.
var supportedHourSteps = []int{1, 2, 3, 4, 6, 8, 12, 24}

// HeartbeatCronExpression returns the busybox-cron expression that fires
// at the given interval. Accepted intervals are listed in the error
// message when the input is unsupported.
func HeartbeatCronExpression(d time.Duration) (string, error) {
	if d < time.Minute {
		return "", supportedSetError(d)
	}
	// Minute-step branch: < 1h and divides 60.
	if d < time.Hour {
		mins := int(d / time.Minute)
		if d != time.Duration(mins)*time.Minute {
			return "", supportedSetError(d)
		}
		for _, n := range supportedMinuteSteps {
			if n == mins {
				return fmt.Sprintf("*/%d * * * *", n), nil
			}
		}
		return "", supportedSetError(d)
	}
	// Hour-step branch: divides 24.
	hours := int(d / time.Hour)
	if d != time.Duration(hours)*time.Hour {
		return "", supportedSetError(d)
	}
	for _, m := range supportedHourSteps {
		if m == hours {
			switch {
			case m == 1:
				return "0 * * * *", nil
			case m == 24:
				return "0 0 * * *", nil
			default:
				return fmt.Sprintf("0 */%d * * *", m), nil
			}
		}
	}
	return "", supportedSetError(d)
}

// ValidateHeartbeatInterval returns nil for an empty string (defaulting
// to DefaultHeartbeatInterval) and for a duration in the supported set;
// otherwise an error explaining the supported values.
func ValidateHeartbeatInterval(s string) error {
	if s == "" {
		return nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if _, err := HeartbeatCronExpression(d); err != nil {
		return err
	}
	return nil
}

func supportedSetError(d time.Duration) error {
	return fmt.Errorf("heartbeat interval %s is not in the supported set "+
		"(supported minute steps: 1m,2m,3m,4m,5m,6m,10m,12m,15m,20m,30m; "+
		"supported hour steps: 1h,2h,3h,4h,6h,8h,12h,24h)", d)
}

// ValidateQuietHours accepts both empty (no quiet hours configured) or
// both set in HH:MM 24-hour form. Wrap-around (start > end) is allowed.
func ValidateQuietHours(start, end string) error {
	if start == "" && end == "" {
		return nil
	}
	if start == "" || end == "" {
		return fmt.Errorf("quiet_start and quiet_end must both be set or neither (got start=%q end=%q)", start, end)
	}
	if _, err := parseHHMM(start); err != nil {
		return fmt.Errorf("quiet_start %q: %w", start, err)
	}
	if _, err := parseHHMM(end); err != nil {
		return fmt.Errorf("quiet_end %q: %w", end, err)
	}
	return nil
}

// ValidateTZ accepts empty (no timezone) or any IANA tz that
// time.LoadLocation accepts.
func ValidateTZ(tz string) error {
	if tz == "" {
		return nil
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Errorf("invalid timezone %q: %w", tz, err)
	}
	return nil
}

// InQuietHours reports whether now falls in [start, end) interpreted in
// loc. Wrap-around (start > end) is supported. Empty start/end yields false.
func InQuietHours(now time.Time, start, end string, loc *time.Location) bool {
	if start == "" || end == "" {
		return false
	}
	startMin, err := parseHHMM(start)
	if err != nil {
		return false
	}
	endMin, err := parseHHMM(end)
	if err != nil {
		return false
	}
	local := now.In(loc)
	cur := local.Hour()*60 + local.Minute()
	if startMin <= endMin {
		return cur >= startMin && cur < endMin
	}
	return cur >= startMin || cur < endMin
}

func parseHHMM(s string) (int, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, fmt.Errorf("not HH:MM")
	}
	hh, err := strconv.Atoi(s[:2])
	if err != nil || hh < 0 || hh > 23 {
		return 0, fmt.Errorf("invalid hour")
	}
	mm, err := strconv.Atoi(s[3:])
	if err != nil || mm < 0 || mm > 59 {
		return 0, fmt.Errorf("invalid minute")
	}
	return hh*60 + mm, nil
}

// ValidateDreamMinInterval accepts empty (use default) or any duration ≥ 1h.
func ValidateDreamMinInterval(s string) error {
	if s == "" {
		return nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	if d < time.Hour {
		return fmt.Errorf("dream_min_interval %s is too short: minimum 1h", d)
	}
	return nil
}

// ValidateMindFormConfig walks every new mind-form config knob and
// returns the first failure. Used by the supervisor at PID-1 startup
// (after Load) so a bad config fails fast with a precise message.
func ValidateMindFormConfig(cfg Config) error {
	if err := ValidateHeartbeatInterval(cfg.Heartbeat.Interval); err != nil {
		return err
	}
	if err := ValidateQuietHours(cfg.MindForm.QuietStart, cfg.MindForm.QuietEnd); err != nil {
		return err
	}
	if err := ValidateTZ(cfg.MindForm.TZ); err != nil {
		return err
	}
	if err := ValidateDreamMinInterval(cfg.MindForm.DreamMinInterval); err != nil {
		return err
	}
	return nil
}
