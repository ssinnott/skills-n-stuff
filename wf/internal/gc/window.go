package gc

// Retention windows as a human writes them.
//
// time.ParseDuration stops at hours, and a retention window is naturally
// spoken in days or weeks — `--before 30d` is what anyone reaches for, and
// `--before 720h` is the same thing said in a way that has to be worked out.
// So days and weeks are accepted on top of the standard suffixes, and
// everything else is handed straight to the standard parser rather than
// reimplemented.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseWindow reads a retention duration. Bare "30d" and "2w" are accepted
// alongside every duration time.ParseDuration understands; an empty string is
// zero, which callers read as "use the default".
func ParseWindow(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	unit := time.Duration(0)
	switch {
	case strings.HasSuffix(s, "d"):
		unit = 24 * time.Hour
	case strings.HasSuffix(s, "w"):
		unit = 7 * 24 * time.Hour
	}
	if unit > 0 {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s[:len(s)-1], " "), 64)
		if err == nil {
			if n < 0 {
				return 0, fmt.Errorf("negative retention window %q", s)
			}
			return time.Duration(n * float64(unit)), nil
		}
		// Fall through: "1m30d" is not a day count, so let the standard
		// parser have its say and report its own error.
	}

	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid retention window %q (try 30d, 2w or 720h)", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("negative retention window %q", s)
	}
	return d, nil
}
