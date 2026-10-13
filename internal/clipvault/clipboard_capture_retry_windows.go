//go:build windows

package clipvault

import (
	"context"
	"errors"
	"time"
)

func readClipboardEventWith(ctx context.Context, cfg Config, expectedSequence uint32,
	readAttempt func(Config, uint32, int) (*ClipboardSnapshot, ClipboardFormatSet, error), sequenceNumber func() uint32,
) (*ClipboardSnapshot, ClipboardFormatSet, int, error) {
	var lastFormats ClipboardFormatSet
	var partial *ClipboardSnapshot
	var partialFormats ClipboardFormatSet
	started := time.Now()
	for attempt, scheduledAt := range clipboardCaptureSchedule {
		attempt++
		if wait := scheduledAt - time.Since(started); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-timer.C:
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return nil, lastFormats, attempt - 1, context.Cause(ctx)
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, lastFormats, attempt - 1, context.Cause(ctx)
		}
		snapshot, formats, err := readAttempt(cfg, expectedSequence, attempt)
		lastFormats = formats
		if errors.Is(err, ErrClipboardSuperseded) {
			return nil, formats, attempt, err
		}
		if ctx.Err() != nil {
			return nil, formats, attempt, context.Cause(ctx)
		}
		if clipboardSequenceSuperseded(expectedSequence, sequenceNumber()) {
			return nil, formats, attempt, ErrClipboardSuperseded
		}
		if err == nil {
			if expectedSequence != 0 {
				snapshot = mergeCompositeRetry(cfg, partial, snapshot)
			}
			if shouldRetryCompositeCapture(snapshot, attempt) {
				// More specific diagnostics can accompany newly recovered
				// formats. Issue count is not a measure of retained content.
				partial, partialFormats = snapshot, formats
				if clipboardSequenceSuperseded(expectedSequence, sequenceNumber()) {
					return nil, formats, attempt, ErrClipboardSuperseded
				}
				continue
			}
			if partial != nil && snapshot == nil {
				return partial, partialFormats, attempt, nil
			}
			if snapshot != nil || len(formats.IDs) > 0 || len(formats.Names) > 0 || attempt >= 2 {
				return snapshot, formats, attempt, nil
			}
			// An empty clipboard gets one short confirmation only.
			continue
		}
		if !shouldRetryClipboardCapture(err, attempt) {
			if partial != nil && isRetryableClipboardReadError(err) {
				if clipboardSequenceSuperseded(expectedSequence, sequenceNumber()) {
					return nil, formats, attempt, ErrClipboardSuperseded
				}
				return partial, partialFormats, attempt, nil
			}
			return nil, formats, attempt, err
		}
		observed := sequenceNumber()
		if clipboardSequenceSuperseded(expectedSequence, observed) {
			return nil, formats, attempt, ErrClipboardSuperseded
		}
	}
	return nil, lastFormats, len(clipboardCaptureSchedule), errors.New("clipboard read attempts exhausted")
}
