package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var weekFlagPatterns = []struct {
	re    *regexp.Regexp
	parse func([]string) (int, int, error)
}{
	{regexp.MustCompile(`^[Ww](\d+)$`), func(m []string) (int, int, error) {
		w, _ := strconv.Atoi(m[1])
		y, _ := time.Now().ISOWeek()
		if w < 1 || w > 53 {
			return 0, 0, fmt.Errorf("week %d out of range (1-53)", w)
		}
		return y, w, nil
	}},
	{regexp.MustCompile(`^(\d{4})-?[Ww](\d+)$`), func(m []string) (int, int, error) {
		y, _ := strconv.Atoi(m[1])
		w, _ := strconv.Atoi(m[2])
		if w < 1 || w > 53 {
			return 0, 0, fmt.Errorf("week %d out of range (1-53)", w)
		}
		return y, w, nil
	}},
	{regexp.MustCompile(`^(\d{1,2})-(\d{1,2})-(\d{4})$`), func(m []string) (int, int, error) {
		mo, _ := strconv.Atoi(m[1])
		d, _ := strconv.Atoi(m[2])
		y, _ := strconv.Atoi(m[3])
		t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
		if t.Year() != y || t.Month() != time.Month(mo) || t.Day() != d {
			return 0, 0, fmt.Errorf("invalid date %s-%s-%s", m[1], m[2], m[3])
		}
		y2, w := t.ISOWeek()
		return y2, w, nil
	}},
	{regexp.MustCompile(`^(\d{1,2})-(\d{1,2})$`), func(m []string) (int, int, error) {
		mo, _ := strconv.Atoi(m[1])
		d, _ := strconv.Atoi(m[2])
		now := time.Now()
		t := time.Date(now.Year(), time.Month(mo), d, 0, 0, 0, 0, time.UTC)
		if t.Month() != time.Month(mo) || t.Day() != d {
			return 0, 0, fmt.Errorf("invalid date %s-%s", m[1], m[2])
		}
		y, w := t.ISOWeek()
		return y, w, nil
	}},
	{regexp.MustCompile(`^-(\d+)$`), func(m []string) (int, int, error) {
		n, _ := strconv.Atoi(m[1])
		t := time.Now().AddDate(0, 0, -n*7)
		y, w := t.ISOWeek()
		return y, w, nil
	}},
}

func parseWeekFlag(s string) (int, int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		y, w := time.Now().ISOWeek()
		return y, w, nil
	}
	for _, pat := range weekFlagPatterns {
		if m := pat.re.FindStringSubmatch(s); len(m) > 0 {
			return pat.parse(m[1:])
		}
	}
	_, err := strconv.Atoi(s)
	if err == nil {
		return 0, 0, fmt.Errorf("ambiguous: use W%s for week or -%s for N weeks ago", s, s)
	}
	return 0, 0, fmt.Errorf("unrecognized week format %q (try W21, 2025-W52, 05-19, 2025-05-19, -3)", s)
}

func addWeekFlag(cmd *cobra.Command) {
	cmd.Flags().String("week", "", `Target ISO week.
  W21          week 21 of current year
  2025-W52     year 2025, week 52
  05-19        May 19 of current year
  2025-05-19   specific date
  -3           3 weeks ago
  (empty)      current week`)
}

func resolveWeek(cmd *cobra.Command) (int, int, error) {
	s, _ := cmd.Flags().GetString("week")
	return parseWeekFlag(s)
}


