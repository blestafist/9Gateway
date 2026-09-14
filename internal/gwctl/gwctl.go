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
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// Version is the version reported by the standalone gwctl binary.
const Version = "0.1.0"

const (
	defaultGatewayURL     = "http://localhost:8080"
	adminKeysPath         = "/admin/v1/keys?limit=1"
	adminKeysEndpoint     = "/admin/v1/keys"
	adminRequestsEndpoint = "/admin/v1/requests"
	requestTimeout        = 10 * time.Second
	maxResponseBytes      = 1 << 20
	keyPageSize           = 50
	requestPageSize       = 50
	maxKeysLimit          = 100000
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
	APIErrorNotFound  APIErrorKind = "not_found"
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
	if command != "version" && command != "ping" && command != "keys" && command != "requests" {
		return usageFailure(stderr, fmt.Errorf("unknown command %q", command))
	}
	if help {
		printUsage(stdout)
		return ExitSuccess
	}
	if command != "keys" && command != "requests" && len(commandArgs) != 1 {
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
	case "keys":
		return runKeys(ctx, commandArgs[1:], options, stdout, stderr)
	case "requests":
		return runRequests(ctx, commandArgs[1:], options, stdout, stderr)
	default:
		// The command set is checked above. Keep this branch defensive if a
		// future edit adds a command without adding its implementation.
		return usageFailure(stderr, fmt.Errorf("unknown command %q", command))
	}
}

type keyListOptions struct {
	limit    int
	limited  bool
	jsonMode bool
}

type keyGetOptions struct {
	jsonMode bool
}

func runKeys(ctx context.Context, args []string, options options, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageFailure(stderr, errors.New("a keys subcommand is required (list or get)"))
	}
	if _, err := validateBaseURL(options.gatewayURL); err != nil {
		return usageFailure(stderr, err)
	}
	if strings.TrimSpace(options.adminCredential) == "" {
		return usageFailure(stderr, errors.New("admin credential is required"))
	}
	switch args[0] {
	case "list":
		parsed, err := parseKeyListOptions(args[1:])
		if err != nil {
			return usageFailure(stderr, err)
		}
		if err := listKeys(ctx, options, parsed, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, describeKeyFailure(err))
			return ExitAPI
		}
		return ExitSuccess
	case "get":
		id, parsed, err := parseKeyGetOptions(args[1:])
		if err != nil {
			return usageFailure(stderr, err)
		}
		if err := getKey(ctx, options, id, parsed, stdout); err != nil {
			fmt.Fprintln(stderr, describeKeyFailure(err))
			return ExitAPI
		}
		return ExitSuccess
	default:
		return usageFailure(stderr, fmt.Errorf("unknown keys subcommand %q", args[0]))
	}
}

func parseKeyListOptions(args []string) (keyListOptions, error) {
	parsed := keyListOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--json":
			parsed.jsonMode = true
		case arg == "--limit":
			if index+1 >= len(args) {
				return keyListOptions{}, errors.New("option --limit requires a value")
			}
			index++
			if err := setKeyListLimit(&parsed, args[index]); err != nil {
				return keyListOptions{}, err
			}
		case strings.HasPrefix(arg, "--limit="):
			if err := setKeyListLimit(&parsed, strings.TrimPrefix(arg, "--limit=")); err != nil {
				return keyListOptions{}, err
			}
		default:
			return keyListOptions{}, fmt.Errorf("unexpected argument for keys list: %s", arg)
		}
	}
	return parsed, nil
}

func setKeyListLimit(parsed *keyListOptions, raw string) error {
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 || limit > maxKeysLimit {
		return fmt.Errorf("--limit must be a positive integer no greater than %d", maxKeysLimit)
	}
	parsed.limit, parsed.limited = limit, true
	return nil
}

func parseKeyGetOptions(args []string) (string, keyGetOptions, error) {
	parsed := keyGetOptions{}
	var id string
	for _, arg := range args {
		if arg == "--json" {
			parsed.jsonMode = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return "", keyGetOptions{}, fmt.Errorf("unknown option for keys get: %s", arg)
		}
		if id != "" {
			return "", keyGetOptions{}, errors.New("keys get requires exactly one key ID")
		}
		id = arg
	}
	if !validKeyID(id) {
		return "", keyGetOptions{}, errors.New("keys get requires a valid key ID")
	}
	return id, parsed, nil
}

func validKeyID(id string) bool {
	if id == "" || len(id) > 256 || id[0] == '-' || id[0] == '_' {
		return false
	}
	for _, character := range id {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

type keyListPage struct {
	Keys       []json.RawMessage `json:"keys"`
	NextCursor *string           `json:"next_cursor"`
}

type aggregatedKeyList struct {
	Keys []json.RawMessage `json:"keys"`
}

func listKeys(ctx context.Context, options options, parsed keyListOptions, stdout, stderr io.Writer) error {
	base, _ := validateBaseURL(options.gatewayURL)
	keys := make([]json.RawMessage, 0)
	cursor := ""
	seenCursors := map[string]struct{}{}
	page := 0
	for {
		remaining := keyPageSize
		if parsed.limited && parsed.limit-len(keys) < remaining {
			remaining = parsed.limit - len(keys)
		}
		if remaining <= 0 {
			break
		}
		query := url.Values{}
		query.Set("limit", strconv.Itoa(remaining))
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		body, err := adminGET(ctx, options, joinURL(base, adminKeysEndpoint+"?"+query.Encode()))
		if err != nil {
			return err
		}
		var response keyListPage
		if err := decodeJSON(body, &response); err != nil {
			return &APIError{Kind: APIErrorMalformed, Err: errors.New("malformed JSON")}
		}
		keys = append(keys, response.Keys...)
		page++
		if parsed.limited && len(keys) >= parsed.limit {
			keys = keys[:parsed.limit]
			break
		}
		if response.NextCursor == nil || *response.NextCursor == "" {
			break
		}
		if _, exists := seenCursors[*response.NextCursor]; exists || (*response.NextCursor == cursor && cursor != "") {
			return &APIError{Kind: APIErrorMalformed, Err: errors.New("repeated pagination cursor")}
		}
		seenCursors[*response.NextCursor] = struct{}{}
		cursor = *response.NextCursor
		if page == 1 {
			fmt.Fprintln(stderr, "Fetching additional key pages...")
		} else {
			fmt.Fprintf(stderr, "Fetching key page %d...\n", page+1)
		}
	}
	if parsed.jsonMode {
		encoded, err := json.Marshal(aggregatedKeyList{Keys: keys})
		if err != nil {
			return &APIError{Kind: APIErrorMalformed, Err: errors.New("could not encode API response")}
		}
		_, _ = fmt.Fprintln(stdout, string(encoded))
		return nil
	}
	if len(keys) == 0 {
		fmt.Fprintln(stdout, "No keys found")
		return nil
	}
	return writeKeyListTable(stdout, keys)
}

func writeKeyListTable(stdout io.Writer, keys []json.RawMessage) error {
	type row struct {
		ID, Name, Prefix, Enabled, Created string
	}
	rows := make([]row, 0, len(keys))
	for _, raw := range keys {
		var item struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Prefix  string `json:"display_prefix"`
			Enabled bool   `json:"enabled"`
			Created string `json:"created_at"`
		}
		if err := decodeJSON(raw, &item); err != nil {
			return &APIError{Kind: APIErrorMalformed, Err: errors.New("malformed JSON")}
		}
		id := item.ID
		if len(id) > 12 {
			id = id[:12]
		}
		rows = append(rows, row{ID: id, Name: item.Name, Prefix: item.Prefix, Enabled: strconv.FormatBool(item.Enabled), Created: localTimestamp(item.Created)})
	}
	writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tName\tPrefix\tEnabled\tCreated")
	for _, item := range rows {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\n", item.ID, item.Name, item.Prefix, item.Enabled, item.Created)
	}
	return writer.Flush()
}

type keyDetail struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Prefix  string     `json:"display_prefix"`
	Enabled *bool      `json:"enabled"`
	Created string     `json:"created_at"`
	Updated string     `json:"updated_at"`
	Expires *string    `json:"expires_at"`
	Policy  *keyPolicy `json:"policy"`
}
type keyPolicy struct {
	AllowedModels         []string    `json:"allowed_models"`
	DeniedModels          []string    `json:"denied_models"`
	RequestWindows        []keyWindow `json:"request_windows"`
	TokenWindows          []keyWindow `json:"token_windows"`
	TokenMode             *string     `json:"token_mode"`
	MaxConcurrentRequests *int        `json:"max_concurrent_requests"`
	BudgetLimits          []keyBudget `json:"budget_limits"`
	LogRequestBody        *bool       `json:"log_request_body"`
	LogResponseBody       *bool       `json:"log_response_body"`
}
type keyWindow struct {
	Amount   *int64 `json:"amount"`
	Duration *int64 `json:"duration"`
}
type keyBudget struct {
	Period       string `json:"period"`
	AmountMicros *int64 `json:"amount_micros"`
}

func getKey(ctx context.Context, options options, id string, parsed keyGetOptions, stdout io.Writer) error {
	base, _ := validateBaseURL(options.gatewayURL)
	endpoint := "/admin/v1/keys/" + url.PathEscape(id)
	body, err := adminGET(ctx, options, joinURL(base, endpoint))
	if err != nil {
		return err
	}
	var detail keyDetail
	if err := decodeJSON(body, &detail); err != nil {
		return &APIError{Kind: APIErrorMalformed, Err: errors.New("malformed JSON")}
	}
	if parsed.jsonMode {
		_, _ = fmt.Fprintln(stdout, string(body))
		return nil
	}
	writeKeyDetail(stdout, detail)
	return nil
}

func writeKeyDetail(stdout io.Writer, detail keyDetail) {
	expires := "-"
	if detail.Expires != nil {
		expires = localTimestamp(*detail.Expires)
	}
	fmt.Fprintf(stdout, "Key\n  ID: %s\n  Name: %s\n  Prefix: %s\n  Enabled: %s\n  Created: %s\n  Updated: %s\n  Expires: %s\n", detail.ID, detail.Name, detail.Prefix, optionalBool(detail.Enabled), localTimestamp(detail.Created), localTimestamp(detail.Updated), expires)
	fmt.Fprintln(stdout, "Policy")
	if detail.Policy == nil {
		fmt.Fprintln(stdout, "  (none)")
		return
	}
	policy := detail.Policy
	fmt.Fprintf(stdout, "  Request limits: %s\n", formatWindows(policy.RequestWindows))
	fmt.Fprintf(stdout, "  Token limits: %s\n", formatWindows(policy.TokenWindows))
	fmt.Fprintf(stdout, "  Token mode: %s\n  Max concurrent requests: %s\n  Budgets: %s\n", optionalString(policy.TokenMode), optionalInt(policy.MaxConcurrentRequests), formatBudgets(policy.BudgetLimits))
	fmt.Fprintf(stdout, "  Allow models: %s\n  Deny models: %s\n", formatStrings(policy.AllowedModels), formatStrings(policy.DeniedModels))
	fmt.Fprintf(stdout, "  Logging: request body=%s, response body=%s\n", optionalBool(policy.LogRequestBody), optionalBool(policy.LogResponseBody))
}

func formatWindows(windows []keyWindow) string {
	if len(windows) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(windows))
	for _, window := range windows {
		parts = append(parts, fmt.Sprintf("%s per %ss", optionalInt64(window.Amount), optionalInt64(window.Duration)))
	}
	return strings.Join(parts, ", ")
}
func formatBudgets(budgets []keyBudget) string {
	if len(budgets) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(budgets))
	for _, budget := range budgets {
		parts = append(parts, fmt.Sprintf("%s=%s micros", budget.Period, optionalInt64(budget.AmountMicros)))
	}
	return strings.Join(parts, ", ")
}
func formatStrings(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ", ")
}
func optionalString(value *string) string {
	if value == nil || *value == "" {
		return "-"
	}
	return *value
}
func optionalBool(value *bool) string {
	if value == nil {
		return "-"
	}
	return strconv.FormatBool(*value)
}
func optionalInt(value *int) string {
	if value == nil {
		return "-"
	}
	return strconv.Itoa(*value)
}
func optionalInt64(value *int64) string {
	if value == nil {
		return "-"
	}
	return strconv.FormatInt(*value, 10)
}
func localTimestamp(raw string) string {
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || raw == "" {
		if raw == "" {
			return "-"
		}
		return raw
	}
	return parsed.Local().Format("2006-01-02 15:04:05 MST")
}

func adminGET(ctx context.Context, options options, target *url.URL) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, &APIError{Kind: APIErrorResponse, Err: errors.New("could not create gateway request")}
	}
	request.Header.Set("Authorization", "Bearer "+options.adminCredential)
	response, err := (&http.Client{Timeout: requestTimeout}).Do(request)
	if err != nil {
		return nil, &APIError{Kind: APIErrorNetwork, Err: err}
	}
	body, readErr := readResponse(response)
	if readErr != nil {
		return nil, &APIError{Kind: APIErrorMalformed, StatusCode: response.StatusCode, Err: readErr}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		kind := APIErrorResponse
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			kind = APIErrorAuth
		}
		if response.StatusCode == http.StatusNotFound {
			kind = APIErrorNotFound
		}
		return nil, &APIError{Kind: kind, StatusCode: response.StatusCode}
	}
	return body, nil
}

func decodeJSON(body []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON")
		}
		return err
	}
	return nil
}

func describeKeyFailure(err error) string {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return "API error: gateway request failed"
	}
	switch apiErr.Kind {
	case APIErrorAuth:
		return "Authentication failed"
	case APIErrorNotFound:
		return "Key not found"
	case APIErrorNetwork:
		// Transport errors can contain a URL, proxy detail, or user-supplied
		// path. Do not reflect that data in CLI diagnostics.
		return "Connection failed"
	case APIErrorMalformed:
		return "Invalid API response"
	case APIErrorResponse:
		return fmt.Sprintf("API error: gateway returned HTTP %d (%s)", apiErr.StatusCode, http.StatusText(apiErr.StatusCode))
	default:
		return "API error: gateway request failed"
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
		return "connection failed"
	case APIErrorAuth:
		return fmt.Sprintf("authentication failed: gateway rejected admin credential (HTTP %d)", apiErr.StatusCode)
	case APIErrorMalformed:
		return "invalid API response"
	case APIErrorResponse:
		if apiErr.StatusCode != 0 {
			return fmt.Sprintf("API error: gateway returned HTTP %d (%s)", apiErr.StatusCode, http.StatusText(apiErr.StatusCode))
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
	fmt.Fprintln(writer, "  keys       list or inspect API keys")
	fmt.Fprintln(writer, "  requests   list or inspect request history")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Keys commands:")
	fmt.Fprintln(writer, "  keys list [--limit N] [--json]")
	fmt.Fprintln(writer, "  keys get <id> [--json]")
	fmt.Fprintln(writer, "  requests list [--limit N] [--key-id ID] [--after TIME] [--before TIME] [--json]")
	fmt.Fprintln(writer, "  requests get <request-id> [--json]")
	fmt.Fprintln(writer, "  requests get <request-id> --body <kind> [--output FILE]")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Global options:")
	fmt.Fprintln(writer, "  --gateway-url URL          gateway base URL (default http://localhost:8080)")
	fmt.Fprintln(writer, "  --admin-credential VALUE   gateway admin credential (or GWCTL_ADMIN_CREDENTIAL)")
	fmt.Fprintln(writer, "  --help                     show this help")
}
