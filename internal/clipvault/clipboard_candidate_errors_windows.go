//go:build windows

package clipvault

import "errors"

// clipboardCandidateErrors keeps the most useful failure after every
// advertised representation has been tried. A delayed-rendering failure must
// survive later decode failures so the capture scheduler still performs its
// bounded retry cycle. Among delayed formats, the first one is the
// highest-priority representation.
type clipboardCandidateErrors struct {
	limit     error
	retryable error
	last      error
}

func (e *clipboardCandidateErrors) add(err error) {
	if err == nil {
		return
	}
	e.last = err
	if e.limit == nil && (errors.Is(err, ErrImageLimitExceeded) || errors.Is(err, ErrTextLimitExceeded)) {
		e.limit = err
	}
	if e.retryable == nil && errors.Is(err, errClipboardDataUnavailable) {
		e.retryable = err
	}
}

func (e clipboardCandidateErrors) err() error {
	if e.limit != nil {
		return e.limit
	}
	if e.retryable != nil {
		return e.retryable
	}
	return e.last
}
