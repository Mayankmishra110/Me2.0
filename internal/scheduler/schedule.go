package scheduler

import (
	"fmt"
	"regexp"
	"strconv"
	"time"
)

var hhmmRe = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

func parseHHMM(s string) (hour, min int, err error) {
	m := hhmmRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, fmt.Errorf("scheduler: time %q: want HH:MM", s)
	}
	hour, _ = strconv.Atoi(m[1])
	min, _ = strconv.Atoi(m[2])
	return hour, min, nil
}

func dailyCronSpec(hhmm string) (string, error) {
	hour, min, err := parseHHMM(hhmm)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d %d * * *", min, hour), nil
}

func previousFire(now time.Time, hour, min int, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	local := now.In(loc)
	today := time.Date(local.Year(), local.Month(), local.Day(), hour, min, 0, 0, loc)
	if local.Before(today) {
		return today.AddDate(0, 0, -1)
	}
	return today
}

// lastScheduledAt is an alias used by cron fire path.
func lastScheduledAt(now time.Time, loc *time.Location, hour, min int) time.Time {
	return previousFire(now, hour, min, loc)
}
