// Package gwctl implements the command-line client for the gateway admin API.
//
// The package deliberately keeps command execution independent of process
// exit handling so callers (and tests) can observe the exact exit status and
// output without starting a subprocess.
package gwctl

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Version is the version reported by the standalone gwctl binary.
const Version = "0.1.0"

const (
	defaultGatewayURL = "http://localhost:8080"
	adminKeysPath     = "/admin/v1/keys?limit=1"
	requestTimeout    = 10 * time.Second
	maxResponseBytes  = 1 << 20
)

// Exit statuses are part of gwctl's command-line interface.
const (
	ExitSuccess = 0
	ExitUsage   = 1
	ExitAPI     = 2
)

// APIErrorKind identifies the class of an admin API failure. Keeping the
// classes separate lets later commands provide more specific diagnostics
// without changing the transport code.
type APIErrorKind string

const (
	APIErrorNetwork   APIErrorKind = "network"
	APIErrorAuth      APIErrorKind = "authentication"
	APIErrorResponse  APIErrorKind = "response"
	APIErrorMalformed APIErrorKind = "malformed_response"
)

// APIError is returned when an API call cannot complete successfully.
type APIError struct {
	Kind       APIErrorKind
	StatusCode int
	Err        error
}

func (err *APIError) Error() string {
	if err.Err != nil {
		return err.Err.Error()
	}
	if err.StatusCode != 0 {
		return fmt.Sprintf("gateway returned HTTP %d", err.StatusCode)
	}
	return "gateway API request failed"
}

func (err *APIError) Unwrap() error { return err.Err }

type options struct {
	gatewayURL      string
	adminCredential string
}

// Run executes gwctl with args and writes ordinary output to stdout and
// diagnostics to stderr. It never calls os.Exit; the returned value is the
// process exit status the binary should use.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if ctx == nil {
		ctx = context.Background()
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	globalArgs, commandArgs, help, err := splitGlobalArgs(args)
	if err != nil {
		return usageFailure(stderr, err)
	}

	options, err := parseOptions(globalArgs)
	if err != nil {
		return usageFailure(stderr, err)
	}
	if len(commandArgs) == 0 {
		if help {
			printUsage(stdout)
			return ExitSuccess
		}
		return usageFailure(stderr, errors.New("a command is required"))
	}

	command := commandArgs[0]
	if command != "version" && command != "ping" {
		return usageFailure(stderr, fmt.Errorf("unknown command %q", command))
	}
	if help {
		printUsage(stdout)
		return ExitSuccess
	}
	if len(commandArgs) != 1 {
		return usageFailure(stderr, fmt.Errorf("unexpected arguments for %s", command))
	}

	switch command {
	case "version":
		fmt.Fprintf(stdout, "gwctl version %s\n", Version)
		return ExitSuccess
	case "ping":
		if err := validatePingOptions(options); err != nil {
			return usageFailure(stderr, err)
		}
		if err := ping(ctx, options); err != nil {
			fmt.Fprintf(stderr, "%s\n", describeFailure(err))
			return ExitAPI
		}
		fmt.Fprintln(stdout, "Gateway reachable and admin authentication succeeded.")
		return ExitSuccess
	default:
		// The command set is checked above. Keep this branch defensive if a
		// future edit adds a command without adding its implementation.
		return usageFailure(stderr, fmt.Errorf("unknown command %q", command))
	}
}

// splitGlobalArgs makes global options work both before and after the
// subcommand, while flag.FlagSet still performs standard option parsing and
// validation. Only the two documented global options are removed here;
// command-specific unknown options remain command arguments and become usage
// errors.
func splitGlobalArgs(args []string) (global, command []string, help bool, err error) {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--help" || arg == "-h" {
			help = true
			continue
		}
		name, value, hasValue, recognized := globalOption(arg)
		if !recognized {
			command = append(command, arg)
			continue
		}
		if !hasValue {
			if index+1 >= len(args) {
				return nil, nil, false, fmt.Errorf("option %s requires a value", name)
			}
			index++
			value = args[index]
		}
		global = append(global, name, value)
	}
	return global, command, help, nil
}

func globalOption(arg string) (name, value string, hasValue, recognized bool) {
	for _, candidate := range []string{"--gateway-url", "--admin-credential"} {
		if arg == candidate {
			return candidate, "", false, true
		}
		prefix := candidate + "="
		if strings.HasPrefix(arg, prefix) {
			return candidate, strings.TrimPrefix(arg, prefix), true, true
		}
	}
	return "", "", false, false
}

func parseOptions(args []string) (options, error) {
	parsed := options{
		gatewayURL:      defaultGatewayURL,
		adminCredential: os.Getenv("GWCTL_ADMIN_CREDENTIAL"),
	}
	flags := flag.NewFlagSet("gwctl", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&parsed.gatewayURL, "gateway-url", parsed.gatewayURL, "gateway base URL (default http://localhost:8080)")
	flags.StringVar(&parsed.adminCredential, "admin-credential", parsed.adminCredential, "gateway admin credential")
	if err := flags.Parse(args); err != nil {
		return options{}, err
	}
	if flags.NArg() != 0 {
		return options{}, errors.New("unexpected global arguments")
	}
	return parsed, nil
}

func ping(ctx context.Context, options options) error {
	base, err := validateBaseURL(options.gatewayURL)
	if err != nil {
		return &APIError{Kind: APIErrorResponse, Err: err}
	}
	if strings.TrimSpace(options.adminCredential) == "" {
		return &APIError{Kind: APIErrorResponse, Err: errors.New("admin credential is required")}
	}

	target := joinURL(base, adminKeysPath)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return &APIError{Kind: APIErrorResponse, Err: errors.New("could not create gateway request")}
	}
	request.Header.Set("Authorization", "Bearer "+options.adminCredential)

	client := &http.Client{Timeout: requestTimeout}
	response, err := client.Do(request)
	if err != nil {
		return &APIError{Kind: APIErrorNetwork, Err: err}
	}
	body, err := readResponse(response)
	if err != nil {
		return &APIError{Kind: APIErrorMalformed, StatusCode: response.StatusCode, Err: err}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		kind := APIErrorResponse
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			kind = APIErrorAuth
		}
		return &APIError{Kind: kind, StatusCode: response.StatusCode}
	}
	if !json.Valid(body) {
		return &APIError{Kind: APIErrorMalformed, StatusCode: response.StatusCode, Err: errors.New("gateway returned malformed JSON")}
	}
	return nil
}

func validatePingOptions(options options) error {
	if _, err := validateBaseURL(options.gatewayURL); err != nil {
		return err
	}
	if strings.TrimSpace(options.adminCredential) == "" {
		return errors.New("admin credential is required")
	}
	return nil
}

func readResponse(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, errors.New("could not read gateway response")
	}
	if len(body) > maxResponseBytes {
		return nil, errors.New("gateway response is too large")
	}
	return body, nil
}

func validateBaseURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("gateway URL is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.IsAbs() == false || parsed.Host == "" || parsed.Opaque != "" {
		return nil, errors.New("gateway URL must be an absolute HTTP or HTTPS URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("gateway URL must use HTTP or HTTPS")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, errors.New("gateway URL must not contain query, fragment, or userinfo")
	}
	if parsed.Hostname() == "" {
		return nil, errors.New("gateway URL must include a host")
	}
	return parsed, nil
}

func joinURL(base *url.URL, endpoint string) *url.URL {
	endpointURL, _ := url.Parse(endpoint) // endpoint is a package constant
	joined := *base
	basePath := strings.TrimRight(base.Path, "/")
	joined.Path = basePath + "/" + strings.TrimLeft(endpointURL.Path, "/")
	if base.RawPath != "" {
		joined.RawPath = strings.TrimRight(base.EscapedPath(), "/") + "/" + strings.TrimLeft(endpointURL.EscapedPath(), "/")
	} else {
		joined.RawPath = ""
	}
	joined.RawQuery = endpointURL.RawQuery
	joined.ForceQuery = endpointURL.ForceQuery
	joined.Fragment = ""
	return &joined
}

func describeFailure(err error) string {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return "API error: gateway request failed"
	}
	switch apiErr.Kind {
	case APIErrorNetwork:
		return fmt.Sprintf("connection failed: %v", apiErr.Err)
	case APIErrorAuth:
		return fmt.Sprintf("authentication failed: gateway rejected admin credential (HTTP %d)", apiErr.StatusCode)
	case APIErrorMalformed:
		if apiErr.Err != nil {
			return "invalid API response: " + apiErr.Err.Error()
		}
		return "invalid API response"
	case APIErrorResponse:
		if apiErr.StatusCode != 0 {
			return fmt.Sprintf("API error: gateway returned HTTP %d (%s)", apiErr.StatusCode, http.StatusText(apiErr.StatusCode))
		}
		if apiErr.Err != nil {
			return "API error: " + apiErr.Err.Error()
		}
		return "API error: gateway request failed"
	default:
		return "API error: gateway request failed"
	}
}

func usageFailure(writer io.Writer, err error) int {
	fmt.Fprintf(writer, "error: %s\n", err)
	printUsage(writer)
	return ExitUsage
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Usage: gwctl [global options] <command>")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Commands:")
	fmt.Fprintln(writer, "  version    print the gwctl version")
	fmt.Fprintln(writer, "  ping       verify gateway connectivity and admin authentication")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Global options:")
	fmt.Fprintln(writer, "  --gateway-url URL          gateway base URL (default http://localhost:8080)")
	fmt.Fprintln(writer, "  --admin-credential VALUE   gateway admin credential (or GWCTL_ADMIN_CREDENTIAL)")
	fmt.Fprintln(writer, "  --help                     show this help")
}
