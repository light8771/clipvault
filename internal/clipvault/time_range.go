package clipvault

import (
	"fmt"
	"time"
)

// localDateFilterBounds 把 UI 的本地日期（2006-01-02）转换为 UTC Unix 毫秒边界：
// from 为当天 00:00（闭区间），before 为结束日次日 00:00（开区间）。0 表示不限制。
func localDateFilterBounds(from, to, timeZone string) (fromMS, beforeMS int64, err error) {
	loc, err := configuredLocation(timeZone)
	if err != nil {
		return 0, 0, err
	}
	if from != "" {
		start, parseErr := time.ParseInLocation("2006-01-02", from, loc)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("invalid start date: %w", parseErr)
		}
		fromMS = start.UTC().UnixMilli()
	}
	if to != "" {
		end, parseErr := time.ParseInLocation("2006-01-02", to, loc)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("invalid end date: %w", parseErr)
		}
		beforeMS = end.AddDate(0, 0, 1).UTC().UnixMilli()
	}
	if fromMS > 0 && beforeMS > 0 && fromMS >= beforeMS {
		return 0, 0, fmt.Errorf("start date must not be after end date")
	}
	return fromMS, beforeMS, nil
}

// normalizeExportRange converts date-only ranges from the configured local
// time zone to UTC instants. The UI's end date is inclusive; RFC3339 callers
// retain the documented exclusive upper-bound semantics.
func normalizeExportRange(scope ExportScope, timeZone string) (ExportScope, error) {
	if scope.Mode != "range" {
		return scope, nil
	}
	_, fromDateErr := time.Parse("2006-01-02", scope.From)
	_, toDateErr := time.Parse("2006-01-02", scope.To)
	if fromDateErr == nil && toDateErr == nil {
		fromMS, beforeMS, err := localDateFilterBounds(scope.From, scope.To, timeZone)
		if err != nil {
			return scope, err
		}
		scope.From = time.UnixMilli(fromMS).UTC().Format(time.RFC3339Nano)
		scope.To = time.UnixMilli(beforeMS).UTC().Format(time.RFC3339Nano)
		return scope, validateExportScope(scope)
	}
	if (fromDateErr == nil) != (toDateErr == nil) {
		return scope, fmt.Errorf("range boundaries must both be dates or both be RFC3339 instants")
	}
	from, err := parseExportInstant(scope.From)
	if err != nil {
		return scope, fmt.Errorf("invalid range start: %w", err)
	}
	to, err := parseExportInstant(scope.To)
	if err != nil {
		return scope, fmt.Errorf("invalid range end: %w", err)
	}
	scope.From = from.UTC().Format(time.RFC3339Nano)
	scope.To = to.UTC().Format(time.RFC3339Nano)
	return scope, validateExportScope(scope)
}

func parseExportInstant(value string) (time.Time, error) {
	return time.Parse(time.RFC3339, value)
}

// rfc3339ToUnixMS 把 RFC3339 瞬间转换为 Unix 毫秒（存储层时间表示）。
func rfc3339ToUnixMS(value string) (int64, error) {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return 0, err
	}
	return t.UTC().UnixMilli(), nil
}
