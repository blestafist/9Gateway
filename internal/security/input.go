// Package security contains small, shared validators for values that cross
// HTTP and configuration boundaries.  They intentionally validate the
// semantic form of a value, rather than applying path cleaning or decoding
// more than the Go URL parser already has.
package security

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

const (
	// MaxIdentifierBytes leaves ample room for externally generated IDs while
	// keeping route and database lookup work bounded.
	MaxIdentifierBytes = 256
	MaxCursorBytes     = 1024
)

// ValidateIdentifier accepts the ASCII identity grammar used by generated
// gateway key IDs and request-related IDs.  In particular, dots, separators,
// controls, and percent escapes are not identity characters.
func ValidateIdentifier(value string) bool {
	if value == "" || len(value) > MaxIdentifierBytes {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			if index == 0 && (character == '-' || character == '_') {
				return false
			}
			continue
		}
		return false
	}
	return true
}

// ValidateRequestID accepts both the current 32-character generated request
// IDs and longer externally supplied identities that can be safely used as a
// lookup key. The minimum preserves the generated-format boundary while the
// maximum keeps route and lookup work bounded.
func ValidateRequestID(value string) bool {
	return len(value) >= 32 && ValidateIdentifier(value)
}

// ValidateKind checks a closed semantic enum.  It does not normalize or
// decode the value, so route parameters cannot smuggle a second kind through
// a second decoding pass.
func ValidateKind(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

// ValidateCursorSyntax performs the inexpensive lexical and canonical
// URL-safe base64 checks before any authenticated cursor is decoded. Empty is
// accepted as the initial-page sentinel; callers decide whether it is allowed
// in their particular query parameter.
func ValidateCursorSyntax(value string) bool {
	if value == "" || len(value) > MaxCursorBytes {
		return value == ""
	}
	if strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return false
	}
	if len(value)%4 == 1 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_') {
			return false
		}
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && base64.RawURLEncoding.EncodeToString(decoded) == value
}

// ValidateUpstreamURL validates the configured 9router origin. Fragments are
// rejected because they are never sent in HTTP requests and silently dropping
// one would make a configuration typo hard to detect. Userinfo is rejected
// universally; the upstream credential belongs in the configured header, not
// in an origin URL.
func ValidateUpstreamURL(value string) (*url.URL, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return nil, fmt.Errorf("%w: URL must not be empty or padded with whitespace", errInvalidUpstreamURL)
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil || !parsed.IsAbs() || parsed.Opaque != "" {
		return nil, fmt.Errorf("%w: URL must be absolute", errInvalidUpstreamURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: scheme must be http or https", errInvalidUpstreamURL)
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return nil, fmt.Errorf("%w: host is required", errInvalidUpstreamURL)
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("%w: embedded credentials are not allowed", errInvalidUpstreamURL)
	}
	if parsed.Fragment != "" {
		return nil, fmt.Errorf("%w: fragments are not allowed", errInvalidUpstreamURL)
	}
	return parsed, nil
}

var errInvalidUpstreamURL = invalidUpstreamURLError{}

type invalidUpstreamURLError struct{}

func (invalidUpstreamURLError) Error() string { return "invalid upstream URL" }
