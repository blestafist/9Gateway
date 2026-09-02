package openai

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
)

// ErrInvalidRequestBodyLimit indicates that request-body inspection was given
// a non-positive byte limit.
var ErrInvalidRequestBodyLimit = errors.New("openai: invalid request body limit")

// InspectRequestBody reads at most limit+1 bytes from body. When available is
// true, inspected contains the complete body and replacement can be used to
// replay the exact bytes. When the body is larger than limit, inspected is nil,
// available is false, and replacement replays the consumed prefix before
// continuing with the unread body.
//
// The input is never closed. A read error is returned with a replacement that
// still preserves any bytes consumed before the error.
func InspectRequestBody(body io.Reader, limit int64) (inspected []byte, replacement io.Reader, available bool, err error) {
	if limit <= 0 {
		return nil, nil, false, fmt.Errorf("%w: %d", ErrInvalidRequestBodyLimit, limit)
	}
	if body == nil {
		return []byte{}, bytes.NewReader(nil), true, nil
	}

	readLimit := limit
	if readLimit < math.MaxInt64 {
		readLimit++
	}
	consumed, readErr := io.ReadAll(io.LimitReader(body, readLimit))
	replacement = io.MultiReader(bytes.NewReader(consumed), body)
	if readErr != nil {
		return nil, replacement, false, fmt.Errorf("openai: read request body for inspection: %w", readErr)
	}
	if int64(len(consumed)) > limit {
		return nil, replacement, false, nil
	}

	return consumed, bytes.NewReader(consumed), true, nil
}
