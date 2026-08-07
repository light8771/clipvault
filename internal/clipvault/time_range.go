package clipvault

import (
	"fmt"
	"time"
)

func localDateFilterBounds(from, to, timeZone string) (createdFrom, createdBefore string, err error) {
	loc, err := configuredLocation(timeZone)
	if err != nil {
		return "", "", err
	}
	if from != "" {
		start, parseErr := time.ParseInLocation("2006-01-02", from, loc)
		if parseErr != nil {
			return "", "", fmt.Errorf("invalid start date: %w", parseErr)
		}
		createdFrom = start.UTC().Format(time.RFC3339Nano)
	}
	if to != "" {
		end, parseErr := time.ParseInLocation("2006-01-02", to, loc)
		if parseErr != nil {
			return "", "", fmt.Errorf("invalid end date: %w", parseErr)
		}
		createdBefore = end.AddDate(0, 0, 1).UTC().Format(time.RFC3339Nano)
	}
	if createdFrom != "" && createdBefore != "" && createdFrom >= createdBefore {
		return "", "", fmt.Errorf("start date must not be after end date")
	}
	return createdFrom, createdBefore, nil
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
		from, before, err := localDateFilterBounds(scope.From, scope.To, timeZone)
		if err != nil {
			return scope, err
		}
		scope.From, scope.To = from, before
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
