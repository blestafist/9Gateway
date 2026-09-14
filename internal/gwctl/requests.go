package gwctl

import (
	"context"
	"encoding/json"
	"errors"
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

type requestListOptions struct {
	limit                int
	limited              bool
	keyID, after, before string
	jsonMode             bool
}

func runRequests(ctx context.Context, args []string, options options, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return usageFailure(stderr, errors.New("a requests subcommand is required (list or get)"))
	}
	if _, err := validateBaseURL(options.gatewayURL); err != nil {
		return usageFailure(stderr, err)
	}
	if strings.TrimSpace(options.adminCredential) == "" {
		return usageFailure(stderr, errors.New("admin credential is required"))
	}
	switch args[0] {
	case "list":
		parsed, err := parseRequestListOptions(args[1:])
		if err != nil {
			return usageFailure(stderr, err)
		}
		if err := listRequests(ctx, options, parsed, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, describeRequestFailure(err))
			return ExitAPI
		}
		return ExitSuccess
	case "get":
		id, parsed, err := parseRequestGetOptions(args[1:])
		if err != nil {
			return usageFailure(stderr, err)
		}
		if parsed.body != "" {
			err = getRequestBody(ctx, options, id, parsed, stdout, stderr)
		} else {
			err = getRequest(ctx, options, id, parsed.jsonMode, stdout)
		}
		if err != nil {
			fmt.Fprintln(stderr, describeRequestFailure(err))
			return ExitAPI
		}
		return ExitSuccess
	default:
		return usageFailure(stderr, fmt.Errorf("unknown requests subcommand %q", args[0]))
	}
}

func parseRequestListOptions(args []string) (requestListOptions, error) {
	var parsed requestListOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			parsed.jsonMode = true
		case arg == "--limit" || arg == "--key-id" || arg == "--after" || arg == "--before":
			if i+1 >= len(args) {
				return parsed, fmt.Errorf("option %s requires a value", arg)
			}
			i++
			if err := setRequestOption(&parsed, arg, args[i]); err != nil {
				return parsed, err
			}
		case strings.HasPrefix(arg, "--limit="):
			if err := setRequestOption(&parsed, "--limit", strings.TrimPrefix(arg, "--limit=")); err != nil {
				return parsed, err
			}
		case strings.HasPrefix(arg, "--key-id="):
			if err := setRequestOption(&parsed, "--key-id", strings.TrimPrefix(arg, "--key-id=")); err != nil {
				return parsed, err
			}
		case strings.HasPrefix(arg, "--after="):
			if err := setRequestOption(&parsed, "--after", strings.TrimPrefix(arg, "--after=")); err != nil {
				return parsed, err
			}
		case strings.HasPrefix(arg, "--before="):
			if err := setRequestOption(&parsed, "--before", strings.TrimPrefix(arg, "--before=")); err != nil {
				return parsed, err
			}
		default:
			return parsed, fmt.Errorf("unexpected argument for requests list: %s", arg)
		}
	}
	if parsed.after != "" && parsed.before != "" {
		a, _ := time.Parse(time.RFC3339, parsed.after)
		b, _ := time.Parse(time.RFC3339, parsed.before)
		if a.After(b) {
			return parsed, errors.New("--after must not be later than --before")
		}
	}
	return parsed, nil
}

func setRequestOption(parsed *requestListOptions, name, value string) error {
	switch name {
	case "--limit":
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 || n > 100000 {
			return errors.New("--limit must be a positive integer no greater than 100000")
		}
		parsed.limit, parsed.limited = n, true
	case "--key-id":
		if !validKeyID(value) {
			return errors.New("--key-id must be a valid key ID")
		}
		parsed.keyID = value
	case "--after", "--before":
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return fmt.Errorf("%s must be RFC3339", name)
		}
		if name == "--after" {
			parsed.after = value
		} else {
			parsed.before = value
		}
	}
	return nil
}

type requestGetOptions struct {
	jsonMode     bool
	body, output string
	outputSet    bool
}

func parseRequestGetOptions(args []string) (string, requestGetOptions, error) {
	var parsed requestGetOptions
	var id string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--json" {
			if parsed.body != "" {
				return "", parsed, errors.New("--json cannot be used with --body")
			}
			parsed.jsonMode = true
			continue
		}
		name := ""
		for _, candidate := range []string{"--body", "--output"} {
			if arg == candidate {
				name = candidate
				if i+1 >= len(args) {
					return "", parsed, fmt.Errorf("option %s requires a value", candidate)
				}
				i++
				arg = args[i]
				break
			}
			if strings.HasPrefix(arg, candidate+"=") {
				name = candidate
				arg = strings.TrimPrefix(arg, candidate+"=")
				break
			}
		}
		if name == "--body" {
			if parsed.body != "" {
				return "", parsed, errors.New("--body may only be specified once")
			}
			if arg != "client_request" && arg != "upstream_request" && arg != "response" {
				return "", parsed, errors.New("--body must be client_request, upstream_request, or response")
			}
			parsed.body = arg
			continue
		}
		if name == "--output" {
			if parsed.outputSet {
				return "", parsed, errors.New("--output may only be specified once")
			}
			if arg == "" {
				return "", parsed, errors.New("--output requires a non-empty path")
			}
			parsed.output, parsed.outputSet = arg, true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return "", parsed, fmt.Errorf("unknown option for requests get: %s", arg)
		}
		if id != "" {
			return "", parsed, errors.New("requests get requires exactly one request ID")
		}
		id = arg
	}
	if parsed.outputSet && parsed.body == "" {
		return "", parsed, errors.New("--output requires --body")
	}
	if parsed.body != "" && parsed.jsonMode {
		return "", parsed, errors.New("--json cannot be used with --body")
	}
	if !validRequestIDForCLI(id) {
		return "", parsed, errors.New("requests get requires a valid request ID")
	}
	return id, parsed, nil
}

func validRequestIDForCLI(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

type requestListPage struct {
	Requests   []json.RawMessage `json:"requests"`
	NextCursor *string           `json:"next_cursor"`
}
type aggregateRequestList struct {
	Requests []json.RawMessage `json:"requests"`
}

func listRequests(ctx context.Context, options options, parsed requestListOptions, stdout, stderr io.Writer) error {
	base, _ := validateBaseURL(options.gatewayURL)
	records := make([]json.RawMessage, 0)
	cursor := ""
	seen := map[string]struct{}{}
	for page := 0; page < maxRequestPages; page++ {
		amount := requestPageSize
		if parsed.limited && parsed.limit-len(records) < amount {
			amount = parsed.limit - len(records)
		}
		if amount <= 0 {
			break
		}
		query := url.Values{}
		query.Set("limit", strconv.Itoa(amount))
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		if parsed.keyID != "" {
			query.Set("key_id", parsed.keyID)
		}
		if parsed.after != "" {
			query.Set("after", parsed.after)
		}
		if parsed.before != "" {
			query.Set("before", parsed.before)
		}
		body, err := adminGET(ctx, options, joinURL(base, adminRequestsEndpoint+"?"+query.Encode()))
		if err != nil {
			return err
		}
		var response requestListPage
		if err := decodeJSON(body, &response); err != nil {
			return &APIError{Kind: APIErrorMalformed, Err: errors.New("malformed JSON")}
		}
		records = append(records, response.Requests...)
		if parsed.limited && len(records) >= parsed.limit {
			records = records[:parsed.limit]
			break
		}
		if response.NextCursor == nil || *response.NextCursor == "" {
			break
		}
		if page+1 >= maxRequestPages {
			return &APIError{Kind: APIErrorMalformed, Err: errors.New("request pagination exceeded 50 pages")}
		}
		if _, ok := seen[*response.NextCursor]; ok || *response.NextCursor == cursor {
			return &APIError{Kind: APIErrorMalformed, Err: errors.New("repeated pagination cursor")}
		}
		seen[*response.NextCursor] = struct{}{}
		cursor = *response.NextCursor
		if page == 0 {
			fmt.Fprintln(stderr, "Fetching additional request pages...")
		}
		fmt.Fprintf(stderr, "Fetching request page %d...\n", page+2)
	}
	if parsed.jsonMode {
		encoded, err := json.Marshal(aggregateRequestList{records})
		if err != nil {
			return &APIError{Kind: APIErrorMalformed, Err: errors.New("could not encode API response")}
		}
		_, err = fmt.Fprintln(stdout, string(encoded))
		return err
	}
	if len(records) == 0 {
		_, err := fmt.Fprintln(stdout, "No requests found")
		return err
	}
	return writeRequestListTable(stdout, records)
}

func writeRequestListTable(stdout io.Writer, records []json.RawMessage) error {
	type row struct{ id, key, method, route, model, status, tokens, cost, duration, completed string }
	rows := make([]row, 0, len(records))
	for _, raw := range records {
		var item requestRecord
		if err := decodeJSON(raw, &item); err != nil {
			return &APIError{Kind: APIErrorMalformed, Err: errors.New("malformed JSON")}
		}
		id := item.RequestID
		if len(id) > 12 {
			id = id[:12]
		}
		route := optionalString(item.Route)
		if route == "-" {
			route = optionalString(item.Path)
		}
		rows = append(rows, row{id, optionalString(item.APIKeyName), optionalString(item.Method), route, optionalString(item.Model), requestStatus(item), optionalNumber(item.TotalTokens, true), formatMicrosCost(item.CostMicros), formatDuration(item.TotalMicros), optionalTime(item.FinishedAt)})
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "Request ID\tKey Name\tMethod\tRoute\tModel\tStatus\tTokens\tCost\tDuration\tCompleted")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.id, r.key, r.method, r.route, r.model, r.status, r.tokens, r.cost, r.duration, r.completed)
	}
	return w.Flush()
}

type requestRecord struct {
	RequestID                   string     `json:"request_id"`
	APIKeyName                  *string    `json:"api_key_name"`
	APIKeyID                    *string    `json:"api_key_id"`
	Method                      *string    `json:"method"`
	Path                        *string    `json:"path"`
	Route                       *string    `json:"route"`
	Model                       *string    `json:"model"`
	RequestedMode               *string    `json:"requested_mode"`
	UpstreamMode                *string    `json:"upstream_mode"`
	DeliveredMode               *string    `json:"delivered_mode"`
	DownstreamStatus            *int64     `json:"downstream_status"`
	UpstreamStatus              *int64     `json:"upstream_status"`
	TerminalOutcome             *string    `json:"terminal_outcome"`
	UpstreamStarted             *bool      `json:"upstream_started"`
	ErrorCode                   *string    `json:"error_code"`
	TotalTokens                 *int64     `json:"total_tokens"`
	InputTokens                 *int64     `json:"input_tokens"`
	OutputTokens                *int64     `json:"output_tokens"`
	CachedInputTokens           *int64     `json:"cached_input_tokens"`
	ReasoningOutputTokens       *int64     `json:"reasoning_output_tokens"`
	CostMicros                  *int64     `json:"cost_micros"`
	FinishedAt                  *time.Time `json:"finished_at"`
	StartedAt                   *time.Time `json:"started_at"`
	UpstreamStartedAt           *time.Time `json:"upstream_started_at"`
	UpstreamHeadersAt           *time.Time `json:"upstream_headers_at"`
	FirstByteAt                 *time.Time `json:"first_byte_at"`
	TotalMicros                 *int64     `json:"total_micros"`
	TimeToUpstreamHeadersMicros *int64     `json:"time_to_upstream_headers_micros"`
	TimeToFirstByteMicros       *int64     `json:"time_to_first_byte_micros"`
	StreamCloseDelayMicros      *int64     `json:"stream_close_delay_micros"`
	HasBodies                   []string   `json:"has_bodies"`
}

func requestStatus(item requestRecord) string {
	if item.DownstreamStatus != nil {
		return strconv.FormatInt(*item.DownstreamStatus, 10)
	}
	if item.UpstreamStatus != nil {
		return strconv.FormatInt(*item.UpstreamStatus, 10)
	}
	if item.TerminalOutcome != nil && *item.TerminalOutcome != "" {
		return *item.TerminalOutcome
	}
	if item.ErrorCode != nil && *item.ErrorCode != "" {
		return *item.ErrorCode
	}
	return "unknown"
}
func optionalNumber(n *int64, commas bool) string {
	if n == nil {
		return "-"
	}
	if commas {
		return formatInteger(*n)
	}
	return strconv.FormatInt(*n, 10)
}
func optionalTime(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04:05 MST")
}
func formatInteger(n int64) string {
	sign := ""
	s := strconv.FormatInt(n, 10)
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return sign + s
}
func formatMicrosCost(n *int64) string {
	if n == nil {
		return "-"
	}
	raw := strconv.FormatInt(*n, 10)
	sign := ""
	if strings.HasPrefix(raw, "-") {
		sign, raw = "-", raw[1:]
	}
	for len(raw) < 7 {
		raw = "0" + raw
	}
	whole, cents := raw[:len(raw)-6], raw[len(raw)-6:len(raw)-4]
	whole = formatIntegerString(whole)
	return sign + "$" + whole + "." + cents
}

func formatIntegerString(value string) string {
	for i := len(value) - 3; i > 0; i -= 3 {
		value = value[:i] + "," + value[i:]
	}
	return value
}
func formatDuration(n *int64) string {
	if n == nil {
		return "-"
	}
	if *n >= 1000000 || *n <= -1000000 {
		return fmt.Sprintf("%d.%03ds", *n/1000000, absInt64(*n)%1000000/1000)
	}
	return fmt.Sprintf("%dms", *n/1000)
}
func absInt64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func getRequest(ctx context.Context, options options, id string, jsonMode bool, stdout io.Writer) error {
	base, _ := validateBaseURL(options.gatewayURL)
	body, err := adminGET(ctx, options, joinURL(base, adminRequestsEndpoint+"/"+url.PathEscape(id)))
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.Kind == APIErrorNotFound {
			apiErr.Err = errors.New("Request not found")
		}
		return err
	}
	var item requestRecord
	if err := decodeJSON(body, &item); err != nil {
		return &APIError{Kind: APIErrorMalformed, Err: errors.New("malformed JSON")}
	}
	if jsonMode {
		_, err = fmt.Fprintln(stdout, string(body))
		return err
	}
	writeRequestDetail(stdout, item)
	return nil
}

func writeRequestDetail(out io.Writer, item requestRecord) {
	fmt.Fprintf(out, "Identity\n  Request ID: %s\n  Key ID: %s\n  Key Name: %s\n\nRequest\n  Method: %s\n  Route: %s\n  Path: %s\n  Model: %s\n  Requested mode: %s\n\nResponse\n  Downstream status: %s\n  Upstream status: %s\n  Upstream mode: %s\n  Delivered mode: %s\n  Upstream started: %s\n  Terminal outcome: %s\n  Error code: %s\n\nUsage and cost\n  Input tokens: %s\n  Output tokens: %s\n  Total tokens: %s\n  Cached input tokens: %s\n  Reasoning output tokens: %s\n  Cost: %s\n\nTiming\n  Started: %s\n  Upstream started: %s\n  Upstream headers: %s\n  First byte: %s\n  Duration: %s\n  Time to upstream headers: %s\n  Time to first byte: %s\n  Stream close delay: %s\n", item.RequestID, optionalString(item.APIKeyID), optionalString(item.APIKeyName), optionalString(item.Method), optionalString(item.Route), optionalString(item.Path), optionalString(item.Model), optionalString(item.RequestedMode), optionalNumber(item.DownstreamStatus, false), optionalNumber(item.UpstreamStatus, false), optionalString(item.UpstreamMode), optionalString(item.DeliveredMode), optionalBool(item.UpstreamStarted), optionalString(item.TerminalOutcome), optionalString(item.ErrorCode), optionalNumber(item.InputTokens, true), optionalNumber(item.OutputTokens, true), optionalNumber(item.TotalTokens, true), optionalNumber(item.CachedInputTokens, true), optionalNumber(item.ReasoningOutputTokens, true), formatMicrosCost(item.CostMicros), optionalTime(item.StartedAt), optionalTime(item.UpstreamStartedAt), optionalTime(item.UpstreamHeadersAt), optionalTime(item.FirstByteAt), formatDuration(item.TotalMicros), formatDuration(item.TimeToUpstreamHeadersMicros), formatDuration(item.TimeToFirstByteMicros), formatDuration(item.StreamCloseDelayMicros))
	if item.ErrorCode != nil && *item.ErrorCode != "" {
		fmt.Fprintf(out, "\nError\n  Code: %s\n", *item.ErrorCode)
	}
}

func getRequestBody(ctx context.Context, options options, id string, parsed requestGetOptions, stdout, stderr io.Writer) error {
	base, _ := validateBaseURL(options.gatewayURL)
	target := joinURL(base, adminRequestsEndpoint+"/"+url.PathEscape(id)+"/bodies/"+url.PathEscape(parsed.body))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return &APIError{Kind: APIErrorResponse, Err: errors.New("could not create gateway request")}
	}
	request.Header.Set("Authorization", "Bearer "+options.adminCredential)
	response, err := (&http.Client{Timeout: requestTimeout}).Do(request)
	if err != nil {
		return &APIError{Kind: APIErrorNetwork, Err: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, maxResponseBytes+1))
		if readErr != nil {
			return &APIError{Kind: APIErrorMalformed, StatusCode: response.StatusCode, Err: readErr}
		}
		kind := APIErrorResponse
		if response.StatusCode == 401 || response.StatusCode == 403 {
			kind = APIErrorAuth
		}
		if response.StatusCode == 404 {
			kind = APIErrorNotFound
		}
		if kind == APIErrorNotFound {
			return &APIError{Kind: kind, StatusCode: response.StatusCode, Err: errors.New("Request body not found")}
		}
		return &APIError{Kind: kind, StatusCode: response.StatusCode}
	}
	if response.ContentLength > maxResponseBytes {
		return &APIError{Kind: APIErrorMalformed, Err: errors.New("response body is too large")}
	}
	original, err := parseBodyHeader(response.Header, "X-Original-Size")
	if err != nil {
		return &APIError{Kind: APIErrorMalformed, Err: errors.New("invalid X-Original-Size header")}
	}
	truncated, err := parseBodyTruncated(response.Header)
	if err != nil {
		return &APIError{Kind: APIErrorMalformed, Err: errors.New("invalid X-Truncated header")}
	}
	if truncated {
		fmt.Fprintln(stderr, "Warning: response body was truncated (original size "+strconv.FormatInt(original, 10)+" bytes).")
	}
	var destination io.Writer = stdout
	var file *os.File
	if parsed.outputSet {
		file, err = os.Create(parsed.output)
		if err != nil {
			return &APIError{Kind: APIErrorResponse, Err: fmt.Errorf("could not open output file: %v", err)}
		}
		destination = file
	}
	_, copyErr := io.Copy(destination, io.LimitReader(response.Body, maxResponseBytes))
	if copyErr == nil {
		var extra [1]byte
		if count, readErr := response.Body.Read(extra[:]); readErr != io.EOF {
			if readErr != nil {
				copyErr = readErr
			} else if count != 0 {
				copyErr = errors.New("response body is too large")
			}
		}
	}
	closeErr := error(nil)
	if file != nil {
		closeErr = file.Close()
	}
	if copyErr != nil {
		return &APIError{Kind: APIErrorMalformed, Err: errors.New("could not read response body")}
	}
	if closeErr != nil {
		return &APIError{Kind: APIErrorResponse, Err: fmt.Errorf("could not close output file: %v", closeErr)}
	}
	return nil
}

func parseBodyHeader(header http.Header, name string) (int64, error) {
	values, ok := header[name]
	if !ok || len(values) != 1 {
		return 0, errors.New("missing header")
	}
	value, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || value < 0 {
		return 0, errors.New("invalid header")
	}
	return value, nil
}
func parseBodyTruncated(header http.Header) (bool, error) {
	values, ok := header["X-Truncated"]
	if !ok || len(values) != 1 {
		return false, errors.New("missing header")
	}
	value, err := strconv.ParseBool(values[0])
	return value, err
}

func describeRequestFailure(err error) string {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return "API error: gateway request failed"
	}
	switch apiErr.Kind {
	case APIErrorAuth:
		return "Authentication failed"
	case APIErrorNotFound:
		if apiErr.Err != nil {
			return apiErr.Err.Error()
		}
		return "Request not found"
	case APIErrorNetwork:
		return fmt.Sprintf("Connection failed: %v", apiErr.Err)
	case APIErrorMalformed:
		return "Invalid API response"
	case APIErrorResponse:
		if apiErr.Err != nil {
			return "API error: " + apiErr.Err.Error()
		}
		return fmt.Sprintf("API error: gateway returned HTTP %d (%s)", apiErr.StatusCode, http.StatusText(apiErr.StatusCode))
	default:
		return "API error: gateway request failed"
	}
}
