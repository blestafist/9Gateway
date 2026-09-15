// Command coveragecheck verifies a Go coverage profile against a statement
// threshold. It reads the profile directly rather than parsing the human
// formatted output of go tool cover, keeping the gate deterministic across
// operating systems and Go versions.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"strconv"
	"strings"
)

type coverageBlockKey struct {
	location   string
	statements int64
}

type coverageBlock struct {
	statements int64
	hits       int64
}

type coverageResult struct {
	statements    int64
	covered       int64
	roundedTenths int64
}

func main() {
	profilePath := flag.String("profile", "", "path to a go test coverage profile")
	minimum := flag.String("min", "80.0", "minimum statement coverage percentage (one decimal place)")
	flag.Parse()
	if *profilePath == "" {
		fail("-profile is required")
	}
	minimumTenths, err := parseThreshold(*minimum)
	if err != nil {
		fail("invalid -min: %v", err)
	}
	file, err := os.Open(*profilePath)
	if err != nil {
		fail("open profile: %v", err)
	}
	defer file.Close()

	result, err := checkProfile(file)
	if err != nil {
		fail("%v", err)
	}
	fmt.Printf("coverage: %d.%d%% (%d/%d statements), threshold: %d.%d%%\n",
		result.roundedTenths/10, result.roundedTenths%10,
		result.covered, result.statements, minimumTenths/10, minimumTenths%10)
	if result.roundedTenths < minimumTenths {
		os.Exit(1)
	}
}

// parseThreshold keeps the gate comparison in integer tenths of a percent.
// In particular, 80.0 is not compared through a binary floating-point value.
func parseThreshold(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return 0, errors.New("must be a non-negative decimal percentage")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" || (len(parts) == 2 && len(parts[1]) > 1) {
		return 0, errors.New("must have at most one digit after the decimal point")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole < 0 {
		return 0, errors.New("must be a non-negative decimal percentage")
	}
	if whole > (int64(^uint64(0)>>1) / 10) {
		return 0, errors.New("is too large")
	}
	tenths := whole * 10
	if len(parts) == 2 && parts[1] != "" {
		if parts[1][0] < '0' || parts[1][0] > '9' {
			return 0, errors.New("must be a non-negative decimal percentage")
		}
		tenths += int64(parts[1][0] - '0')
	}
	return tenths, nil
}

func checkProfile(reader io.Reader) (coverageResult, error) {
	var result coverageResult
	blocks := make(map[coverageBlockKey]coverageBlock)
	scanner := bufio.NewScanner(reader)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return result, fmt.Errorf("read profile: %w", err)
		}
		return result, errors.New("coverage profile is missing its mode line")
	}
	modeLine := scanner.Text()
	if !strings.HasPrefix(modeLine, "mode: ") {
		return result, fmt.Errorf("invalid mode line %q", modeLine)
	}
	mode := strings.TrimPrefix(modeLine, "mode: ")
	if mode != "set" && mode != "count" && mode != "atomic" {
		return result, fmt.Errorf("unsupported coverage mode %q", mode)
	}

	for scanner.Scan() {
		location, statements, hits, err := parseCoverageLine(scanner.Text())
		if err != nil {
			return result, err
		}
		key := coverageBlockKey{location: location, statements: statements}
		block := blocks[key]
		block.statements = statements
		// Any positive duplicate count covers the block. Taking the maximum
		// also avoids count-mode overflow and is equivalent for this decision.
		if hits > block.hits {
			block.hits = hits
		}
		blocks[key] = block
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("read profile: %w", err)
	}
	for _, block := range blocks {
		if block.statements > 0 && result.statements > int64(^uint64(0)>>1)-block.statements {
			return result, errors.New("coverage statement total overflows int64")
		}
		result.statements += block.statements
		if block.hits > 0 {
			if result.covered > int64(^uint64(0)>>1)-block.statements {
				return result, errors.New("covered statement total overflows int64")
			}
			result.covered += block.statements
		}
	}
	if result.statements == 0 {
		return result, errors.New("coverage profile contains no statements")
	}
	result.roundedTenths = roundedCoverageTenths(result.covered, result.statements)
	return result, nil
}

func parseCoverageLine(line string) (location string, statements, hits int64, err error) {
	line = strings.TrimSpace(line)
	lastSpace := strings.LastIndexByte(line, ' ')
	if lastSpace <= 0 {
		return "", 0, 0, fmt.Errorf("invalid coverage profile line %q", line)
	}
	hits, err = parseProfileInteger(strings.TrimSpace(line[lastSpace+1:]), "execution count")
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid coverage profile line %q: %w", line, err)
	}
	line = strings.TrimSpace(line[:lastSpace])
	lastSpace = strings.LastIndexByte(line, ' ')
	if lastSpace <= 0 {
		return "", 0, 0, fmt.Errorf("invalid coverage profile line %q", line)
	}
	statements, err = parseProfileInteger(strings.TrimSpace(line[lastSpace+1:]), "statement count")
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid coverage profile line %q: %w", line, err)
	}
	location = strings.TrimSpace(line[:lastSpace])
	if err := validateLocation(location); err != nil {
		return "", 0, 0, fmt.Errorf("invalid coverage profile line %q: %w", line, err)
	}
	return location, statements, hits, nil
}

func parseProfileInteger(value, name string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return parsed, nil
}

func validateLocation(location string) error {
	colon := strings.LastIndexByte(location, ':')
	if colon <= 0 || colon == len(location)-1 {
		return errors.New("invalid source range")
	}
	coordinates := strings.Split(location[colon+1:], ",")
	if len(coordinates) != 2 {
		return errors.New("invalid source range")
	}
	for _, coordinate := range coordinates {
		parts := strings.Split(coordinate, ".")
		if len(parts) != 2 {
			return errors.New("invalid source range")
		}
		for _, part := range parts {
			if _, err := parseProfileInteger(part, "source coordinate"); err != nil {
				return errors.New("invalid source range")
			}
		}
	}
	return nil
}

func roundedCoverageTenths(covered, statements int64) int64 {
	// Round exactly as go tool cover's one-decimal %.1f output does, without
	// allowing a binary floating-point representation to move a half-way case.
	numerator := new(big.Int).Mul(big.NewInt(covered), big.NewInt(1000))
	denominator := big.NewInt(statements)
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	doubledRemainder := new(big.Int).Lsh(new(big.Int).Set(remainder), 1)
	if doubledRemainder.Cmp(denominator) > 0 ||
		(doubledRemainder.Cmp(denominator) == 0 && quotient.Bit(0) != 0) {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient.Int64()
}

func fail(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "coveragecheck: "+format+"\n", arguments...)
	os.Exit(2)
}
