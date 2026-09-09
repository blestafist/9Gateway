// Package modelmatch contains the gateway's slash-aware model glob compiler.
// A star, question mark, or character class never crosses a model path slash;
// a backslash quotes the following metacharacter.
package modelmatch

import (
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

var ErrInvalidPattern = errors.New("invalid model pattern")

type tokenKind uint8

const (
	literal tokenKind = iota
	star
	question
	class
)

type classRange struct{ lo, hi rune }

type token struct {
	kind    tokenKind
	literal rune
	negated bool
	ranges  []classRange
}

// Pattern is an immutable compiled model selector.
type Pattern struct {
	source string
	tokens []token
	key    string
	exact  bool
}

func Compile(source string) (Pattern, error) {
	if source == "" || !utf8.ValidString(source) {
		return Pattern{}, ErrInvalidPattern
	}
	runes := []rune(source)
	tokens := make([]token, 0, len(runes))
	for index := 0; index < len(runes); index++ {
		switch runes[index] {
		case '\\':
			if index+1 >= len(runes) {
				return Pattern{}, ErrInvalidPattern
			}
			index++
			tokens = append(tokens, token{kind: literal, literal: runes[index]})
		case '*':
			if len(tokens) == 0 || tokens[len(tokens)-1].kind != star {
				tokens = append(tokens, token{kind: star})
			}
		case '?':
			tokens = append(tokens, token{kind: question})
		case '[':
			parsed, next, err := parseClass(runes, index)
			if err != nil {
				return Pattern{}, ErrInvalidPattern
			}
			tokens = append(tokens, parsed)
			index = next
		default:
			tokens = append(tokens, token{kind: literal, literal: runes[index]})
		}
	}
	pattern := Pattern{source: source, tokens: tokens, exact: true}
	for _, item := range tokens {
		if item.kind != literal {
			pattern.exact = false
			break
		}
	}
	pattern.key = canonicalKey(tokens)
	return pattern, nil
}

func parseClass(runes []rune, start int) (token, int, error) {
	index := start + 1
	parsed := token{kind: class}
	if index < len(runes) && (runes[index] == '!' || runes[index] == '^') {
		parsed.negated = true
		index++
	}
	if index >= len(runes) || runes[index] == ']' {
		return token{}, 0, ErrInvalidPattern
	}
	for index < len(runes) && runes[index] != ']' {
		lo, next, err := classRune(runes, index)
		if err != nil {
			return token{}, 0, err
		}
		index = next
		hi := lo
		if index < len(runes) && runes[index] == '-' {
			if index+1 >= len(runes) || runes[index+1] == ']' {
				return token{}, 0, ErrInvalidPattern
			}
			hi, index, err = classRune(runes, index+1)
			if err != nil || hi < lo {
				return token{}, 0, ErrInvalidPattern
			}
		}
		parsed.ranges = append(parsed.ranges, classRange{lo: lo, hi: hi})
	}
	if index >= len(runes) || len(parsed.ranges) == 0 {
		return token{}, 0, ErrInvalidPattern
	}
	return parsed, index, nil
}

func classRune(runes []rune, index int) (rune, int, error) {
	if runes[index] == '\\' {
		if index+1 >= len(runes) {
			return 0, 0, ErrInvalidPattern
		}
		return runes[index+1], index + 2, nil
	}
	if runes[index] == '[' {
		return 0, 0, ErrInvalidPattern
	}
	return runes[index], index + 1, nil
}

func (pattern Pattern) Source() string { return pattern.source }

func (pattern Pattern) IsExact() bool { return pattern.exact }

// Key identifies the normalized compiled selector. It is intended only for
// duplicate detection at configuration load, not as a public matching API.
func (pattern Pattern) Key() string { return pattern.key }

func (pattern Pattern) Matches(value string) bool {
	valueRunes := []rune(value)
	previous := make([]bool, len(valueRunes)+1)
	previous[0] = true
	for _, item := range pattern.tokens {
		current := make([]bool, len(valueRunes)+1)
		for valueIndex := 0; valueIndex <= len(valueRunes); valueIndex++ {
			if item.kind == star {
				current[valueIndex] = previous[valueIndex] || valueIndex > 0 && valueRunes[valueIndex-1] != '/' && current[valueIndex-1]
			} else if valueIndex > 0 && previous[valueIndex-1] && tokenMatches(item, valueRunes[valueIndex-1]) {
				current[valueIndex] = true
			}
		}
		previous = current
	}
	return previous[len(valueRunes)]
}

func tokenMatches(item token, value rune) bool {
	switch item.kind {
	case literal:
		return item.literal == value
	case question:
		return value != '/'
	case class:
		if value == '/' {
			return false
		}
		matched := false
		for _, bounds := range item.ranges {
			if value >= bounds.lo && value <= bounds.hi {
				matched = true
				break
			}
		}
		if item.negated {
			return !matched
		}
		return matched
	default:
		return false
	}
}

func canonicalKey(tokens []token) string {
	var builder strings.Builder
	for _, item := range tokens {
		switch item.kind {
		case literal:
			builder.WriteString("l:")
			builder.WriteRune(item.literal)
		case star:
			builder.WriteString("s;")
		case question:
			builder.WriteString("q;")
		case class:
			builder.WriteString("c:")
			if item.negated {
				builder.WriteByte('!')
			} else {
				builder.WriteByte('=')
			}
			ranges := append([]classRange(nil), item.ranges...)
			sort.Slice(ranges, func(i, j int) bool {
				if ranges[i].lo == ranges[j].lo {
					return ranges[i].hi < ranges[j].hi
				}
				return ranges[i].lo < ranges[j].lo
			})
			merged := ranges[:0]
			for _, current := range ranges {
				if len(merged) > 0 && current.lo <= merged[len(merged)-1].hi+1 {
					if current.hi > merged[len(merged)-1].hi {
						merged[len(merged)-1].hi = current.hi
					}
					continue
				}
				merged = append(merged, current)
			}
			for _, bounds := range merged {
				builder.WriteRune(bounds.lo)
				builder.WriteByte('-')
				builder.WriteRune(bounds.hi)
				builder.WriteByte(',')
			}
			builder.WriteByte(';')
		}
	}
	return builder.String()
}
