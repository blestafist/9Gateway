package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/config"
	"github.com/pestit/9gateway/internal/httpserver"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
	"github.com/pestit/9gateway/internal/version"
)

func main() {
	if err := run(); err != nil {
		slog.Default().Error("gateway startup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		return runHealthcheck(os.Args[2:])
	}
	for _, arg := range os.Args[1:] {
		if arg == "--version" {
			version.Format(os.Stdout, "gateway")
			return nil
		}
	}
	configPath := flag.String("config", "", "path to the gateway YAML configuration")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	pricingResolver := accounting.NewPricingResolver(cfg.Pricing)
	budgetLimiter := limiter.NewBudgetLimiter()
	database, err := storage.Open(context.Background(), cfg.SQLitePath)
	if err != nil {
		return err
	}
	shutdownStarted := false
	defer func() {
		// shutdownGateway owns the final close once the listener has started. If
		// shutdown is forced while a handler survives, deliberately leave the
		// database open: a deferred close here would race that handler.
		if !shutdownStarted {
			if err := database.Close(); err != nil {
				slog.Default().Error("SQLite shutdown failed", "error", err)
			}
		}
	}()
	keyRepository := storage.NewAPIKeyRepository(database)
	historyRepository := storage.NewRequestHistoryRepository(database)
	keyRecords, err := keyRepository.List(context.Background())
	if err != nil {
		return err
	}
	tokenLimiter := limiter.NewTokenLimiter(nil)
	usageRepository := storage.NewUsageBucketRepository(database)
	budgetRepository := storage.NewBudgetBucketRepository(database)
	now := time.Now().UTC()
	if err := usageRepository.DeleteExpired(context.Background(), now); err != nil {
		return err
	}
	legacy, err := usageRepository.LoadLegacyUnexpired(context.Background(), now)
	if err != nil {
		return err
	}
	for _, bucket := range legacy {
		policyRecord, found := keyRecordByID(keyRecords, bucket.APIKeyID)
		if !found {
			return errors.New("startup: legacy persisted token bucket has unknown key")
		}
		policy, policyErr := auth.ParsePolicyJSONWithTokenMode([]byte(policyRecord.PolicyJSON), auth.TokenMode(cfg.Tokenizer.Mode))
		if policyErr != nil {
			return policyErr
		}
		var match auth.TokenWindow
		for _, candidate := range policy.TokenWindows() {
			if int64(candidate.Duration/time.Second) == bucket.BucketSeconds {
				if match.Duration != 0 {
					return errors.New("startup: legacy persisted token bucket has ambiguous policy duration")
				}
				match = candidate
			}
		}
		if match.Duration == 0 {
			return errors.New("startup: legacy persisted token bucket does not match key policy")
		}
		if err := usageRepository.PromoteLegacy(context.Background(), bucket, match.Amount); err != nil {
			return err
		}
	}
	if err := usageRepository.CompleteLegacyMigration(context.Background()); err != nil {
		return err
	}
	persisted, err := usageRepository.LoadUnexpired(context.Background(), now)
	if err != nil {
		return err
	}
	allowedWindows := make(map[string]map[limiter.TokenWindow]struct{}, len(keyRecords))
	for _, record := range keyRecords {
		policy, policyErr := auth.ParsePolicyJSONWithTokenMode([]byte(record.PolicyJSON), auth.TokenMode(cfg.Tokenizer.Mode))
		if policyErr != nil {
			return policyErr
		}
		windows := make(map[limiter.TokenWindow]struct{})
		for _, window := range policy.TokenWindows() {
			windows[window] = struct{}{}
		}
		allowedWindows[record.ID] = windows
	}
	committed := make([]limiter.CommittedTokenBucket, 0, len(persisted))
	for _, bucket := range persisted {
		// The storage row carries only the fixed-window width. Its capacity is
		// recovered from the key policy; a row for an unknown key/window is
		// inconsistent and must fail closed at startup.
		windows := allowedWindows[bucket.APIKeyID]
		var matched limiter.TokenWindow
		for candidate := range windows {
			if int64(candidate.Duration/time.Second) == bucket.BucketSeconds && candidate.Amount == bucket.BucketAmount {
				matched = candidate
				break
			}
		}
		if matched.Duration == 0 {
			return errors.New("startup: persisted token bucket does not match key policy")
		}
		committed = append(committed, limiter.CommittedTokenBucket{KeyID: bucket.APIKeyID, BucketStart: bucket.BucketStart, Window: matched, CommittedTokens: bucket.CommittedTokens})
	}
	if err := tokenLimiter.LoadCommitted(time.Now().UTC(), committed); err != nil {
		return err
	}
	persistedBudget, err := budgetRepository.LoadTotal(context.Background())
	if err != nil {
		return err
	}
	if err := budgetRepository.DeleteExpiredDays(context.Background(), now); err != nil {
		return err
	}
	if err := budgetRepository.DeleteExpiredMonths(context.Background(), now); err != nil {
		return err
	}
	persistedDays, err := budgetRepository.LoadDay(context.Background(), now)
	if err != nil {
		return err
	}
	persistedMonths, err := budgetRepository.LoadMonth(context.Background(), now)
	if err != nil {
		return err
	}
	knownBudgetKeys := make(map[string]struct{}, len(keyRecords))
	for _, record := range keyRecords {
		knownBudgetKeys[record.ID] = struct{}{}
	}
	spent := make([]limiter.BudgetSpent, 0, len(persistedBudget))
	for _, bucket := range persistedBudget {
		if _, found := knownBudgetKeys[bucket.APIKeyID]; !found {
			return errors.New("startup: persisted budget bucket has unknown key")
		}
		value, valueErr := accounting.NewMoneyMicros(bucket.SpentMicros)
		if valueErr != nil {
			return errors.New("startup: persisted budget bucket is invalid")
		}
		spent = append(spent, limiter.BudgetSpent{KeyID: bucket.APIKeyID, Spent: value})
	}
	for _, bucket := range persistedDays {
		value, valueErr := accounting.NewMoneyMicros(bucket.SpentMicros)
		if valueErr != nil {
			return errors.New("startup: persisted daily budget bucket is invalid")
		}
		spent = append(spent, limiter.BudgetSpent{KeyID: bucket.APIKeyID, Spent: value, Period: limiter.BudgetPeriodDay, PeriodStart: bucket.PeriodStart})
	}
	for _, bucket := range persistedMonths {
		value, valueErr := accounting.NewMoneyMicros(bucket.SpentMicros)
		if valueErr != nil {
			return errors.New("startup: persisted monthly budget bucket is invalid")
		}
		spent = append(spent, limiter.BudgetSpent{KeyID: bucket.APIKeyID, Spent: value, Period: limiter.BudgetPeriodMonth, PeriodStart: bucket.PeriodStart})
	}
	if err := budgetLimiter.LoadSpent(spent); err != nil {
		return err
	}
	processContext, processCancel := context.WithCancel(context.Background())
	defer processCancel()
	aggregateAccumulator := storage.NewUsageAggregateAccumulatorWithContext(processContext, usageRepository)
	tokenLimiter.SetCommittedDeltaSink(aggregateAccumulator.Sink)
	budgetAccumulator := storage.NewBudgetAccumulatorWithContext(processContext, budgetRepository)
	budgetLimiter.SetCommittedDeltaSink(budgetAccumulator.Sink)

	upstreamClient := transport.NewClient()
	completionLogger := httpserver.NewCompletionLogger(slog.Default(), cfg.Observability.TelemetryQueueCapacity)
	usageObservationWorker := httpserver.NewUsageObservationWorker(httpserver.UsageObservationWorkerOptions{Capacity: cfg.Observability.TelemetryQueueCapacity})
	historyWorker := httpserver.NewHistoryPersistenceWorker(httpserver.HistoryPersistenceWorkerOptions{
		Repository:       historyRepository,
		Capacity:         cfg.Observability.TelemetryQueueCapacity,
		RequestRetention: time.Duration(cfg.Observability.RequestRetentionSeconds) * time.Second,
		BodyRetention:    time.Duration(cfg.Observability.BodyRetentionSeconds) * time.Second,
	})
	if err := historyWorker.WaitReady(context.Background()); err != nil {
		cleanupStartup(database, usageObservationWorker, aggregateAccumulator, budgetAccumulator, completionLogger, historyWorker)
		return err
	}
	readinessState := &httpserver.ReadinessState{}

	gatewayHandler, err := httpserver.NewHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorkerAndHistory(upstreamClient, cfg.UpstreamBaseURL, cfg.UpstreamAPIKey, cfg.AdminCredential, cfg.AuthPepper, keyRepository, nil, nil, completionLogger, tokenLimiter, httpserver.TokenAdmissionConfig{
		MaxInspectedRequestBytes:   cfg.Tokenizer.MaxInspectedRequestBytes,
		MaxCapturedBodyBytes:       cfg.Observability.MaxCapturedBodyBytes,
		FallbackUnknownInputTokens: cfg.Tokenizer.FallbackUnknownInputTokens,
		FallbackMaxOutputTokens:    cfg.Tokenizer.FallbackMaxOutputTokens,
		PricingResolver:            pricingResolver,
		BudgetLimiter:              budgetLimiter,
	}, usageObservationWorker, historyWorker, auth.TokenMode(cfg.Tokenizer.Mode))
	if err != nil {
		cleanupStartup(database, usageObservationWorker, aggregateAccumulator, budgetAccumulator, completionLogger, historyWorker)
		return err
	}
	gatewayHandler = httpserver.WithReadiness(gatewayHandler, httpserver.NewReadiness(httpserver.ReadinessConfig{
		Database:               database,
		UpstreamBaseURL:        cfg.UpstreamBaseURL,
		UsageObservationWorker: usageObservationWorker,
		State:                  readinessState,
	}))
	metadata := version.Current()
	slog.Default().Info("starting gateway version=" + metadata.Version + " commit=" + metadata.Commit + " build=" + metadata.BuildDate)
	activeRequests := httpserver.NewRequestLifecycle()
	trackedHandler := activeRequests.Handler(gatewayHandler)
	server := &http.Server{
		Addr:           cfg.ListenAddr,
		Handler:        trackedHandler,
		MaxHeaderBytes: 16 * 1024,
	}
	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	var shutdownResult error
	select {
	case err := <-serveErr:
		stop()
		if err != nil && err != http.ErrServerClosed {
			slog.Default().Error("HTTP server failed", "error", err)
			cleanupStartup(nil, usageObservationWorker, aggregateAccumulator, budgetAccumulator, completionLogger, historyWorker)
			return err
		}
	case <-shutdownContext.Done():
		// Restore the default signal disposition immediately. A second SIGTERM
		// or SIGINT must be able to terminate a process stuck in a handler.
		stop()
		shutdownStarted = true
		shutdownErr := shutdownGateway(cfg.ShutdownTimeoutSeconds, server, database, readinessState, activeRequests,
			usageObservationWorker, aggregateAccumulator, budgetAccumulator, completionLogger, historyWorker)
		shutdownResult = shutdownErr
		if shutdownErr != nil {
			slog.Default().Error("gateway shutdown failed", "error", shutdownErr)
		}
		if err := <-serveErr; err != nil && err != http.ErrServerClosed {
			slog.Default().Error("HTTP server failed", "error", err)
		}
	}
	return shutdownResult
}

var errShutdownDeadline = errors.New("gateway shutdown deadline exceeded; dependent resources remain open")

const (
	defaultHealthcheckAddress = "http://127.0.0.1:8080/ready"
	healthcheckTimeout        = 2 * time.Second
)

// runHealthcheck is intentionally a small, dependency-free probe for the
// container HEALTHCHECK instruction. It checks readiness rather than merely
// process liveness so an image orchestrator does not route traffic to a
// gateway whose storage or lifecycle is unavailable.
func runHealthcheck(args []string) error {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	address := os.Getenv("GATEWAY_HEALTHCHECK_ADDRESS")
	if address == "" {
		address = defaultHealthcheckAddress
	}
	flags.StringVar(&address, "address", address, "gateway readiness URL or host:port")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("healthcheck does not accept positional arguments")
	}
	target, err := readinessURL(address)
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("create readiness request: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()
	request = request.WithContext(ctx)
	response, err := (&http.Client{Timeout: healthcheckTimeout}).Do(request)
	if err != nil {
		return fmt.Errorf("readiness request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness returned HTTP %d", response.StatusCode)
	}
	return nil
}

func readinessURL(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", errors.New("healthcheck address is required")
	}
	if !strings.Contains(address, "://") {
		if strings.HasPrefix(address, ":") {
			address = "127.0.0.1" + address
		}
		return "http://" + address + "/ready", nil
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("healthcheck address must be an HTTP URL or host:port")
	}
	parsed.Path = "/ready"
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func cleanupStartup(_ *storage.DB, usage *httpserver.UsageObservationWorker, token *storage.UsageAggregateAccumulator, budget *storage.BudgetAccumulator, completion *httpserver.CompletionLogger, history *httpserver.HistoryPersistenceWorker) {
	ctx := context.Background()
	_ = usage.Shutdown(ctx)
	_ = token.Shutdown(ctx)
	_ = budget.Shutdown(ctx)
	_ = completion.Shutdown(ctx)
	_ = history.Shutdown(ctx)
}

// shutdownGateway coordinates the process-owned dependencies under one
// absolute deadline. Readiness is flipped before net/http stops accepting.
func shutdownGateway(timeoutSeconds int64, server *http.Server, database *storage.DB, readiness *httpserver.ReadinessState, active *httpserver.RequestLifecycle,
	usage *httpserver.UsageObservationWorker, token *storage.UsageAggregateAccumulator, budget *storage.BudgetAccumulator,
	completion *httpserver.CompletionLogger, history *httpserver.HistoryPersistenceWorker) error {
	if timeoutSeconds <= 0 {
		timeoutSeconds = config.DefaultShutdownTimeoutSeconds
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
	defer cancel()
	if readiness != nil {
		readiness.MarkDraining()
	}
	slog.Default().Info("shutting down HTTP server")
	shutdownErr := server.Shutdown(ctx)
	if shutdownErr != nil {
		if err := server.Close(); err != nil {
			slog.Default().Error("HTTP server close failed", "error", err)
		}
		slog.Default().Error("HTTP server shutdown failed", "error", shutdownErr)
	}
	if active != nil {
		active.StopAccepting()
		if err := active.Wait(ctx); err != nil {
			// Close has already cancelled context-aware handlers. Never close
			// workers or SQLite while a non-cooperative handler can still use
			// them.
			return errors.Join(shutdownErr, errShutdownDeadline, err)
		}
	}
	var lifecycleErr error
	if usage != nil {
		if err := usage.Drain(ctx); err != nil {
			slog.Default().Error("usage observation shutdown failed", "error", err)
			if ctx.Err() != nil {
				return errors.Join(shutdownErr, errShutdownDeadline, err)
			}
			lifecycleErr = errors.Join(lifecycleErr, err)
		}
	}
	if token != nil {
		if err := token.Shutdown(ctx); err != nil {
			slog.Default().Error("token aggregate shutdown failed", "error", err)
			if ctx.Err() != nil {
				return errors.Join(shutdownErr, errShutdownDeadline, err)
			}
			lifecycleErr = errors.Join(lifecycleErr, err)
		}
	}
	if budget != nil {
		if err := budget.Shutdown(ctx); err != nil {
			slog.Default().Error("budget aggregate shutdown failed", "error", err)
			if ctx.Err() != nil {
				return errors.Join(shutdownErr, errShutdownDeadline, err)
			}
			lifecycleErr = errors.Join(lifecycleErr, err)
		}
	}
	pending := 0
	if completion != nil {
		pending += completion.Pending()
	}
	if history != nil {
		pending += history.Pending()
	}
	slog.Default().Info("draining telemetry", "pending", pending)
	var drainErr error
	if completion != nil {
		drainErr = errors.Join(drainErr, completion.Shutdown(ctx))
	}
	if history != nil {
		drainErr = errors.Join(drainErr, history.Shutdown(ctx))
	}
	dropped := uint64(0)
	if usage != nil {
		dropped += usage.Dropped()
	}
	if completion != nil {
		dropped += completion.Dropped()
	}
	if history != nil {
		dropped += history.Dropped()
	}
	// A timed-out owner may still be inside repository code. Closing SQLite in
	// that case would race the owner, so leave the handle to the operating
	// system as the process exits. This check deliberately covers every worker
	// and aggregate saver that can issue database calls, not just history.
	if !doneClosed(history.Done()) || !doneClosed(token.Done()) || !doneClosed(budget.Done()) {
		return errors.Join(shutdownErr, errShutdownDeadline, drainErr)
	}
	slog.Default().Info("closing storage", "dropped", dropped)
	if database != nil {
		if err := database.Close(); err != nil {
			drainErr = errors.Join(drainErr, err)
		}
	}
	if drainErr != nil {
		slog.Default().Error("telemetry shutdown failed", "error", drainErr)
		if ctx.Err() != nil {
			return errors.Join(shutdownErr, errShutdownDeadline, drainErr)
		}
		lifecycleErr = errors.Join(lifecycleErr, drainErr)
	}
	slog.Default().Info("shutdown complete", "dropped", dropped)
	return errors.Join(shutdownErr, lifecycleErr)
}

func doneClosed(done <-chan struct{}) bool {
	if done == nil {
		return true
	}
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func keyRecordByID(records []storage.APIKeyRecord, id string) (storage.APIKeyRecord, bool) {
	for _, record := range records {
		if record.ID == id {
			return record, true
		}
	}
	return storage.APIKeyRecord{}, false
}
