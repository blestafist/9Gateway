package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/config"
	"github.com/pestit/9gateway/internal/httpserver"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

func main() {
	if err := run(); err != nil {
		slog.Default().Error("gateway startup failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
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
	defer func() {
		if err := database.Close(); err != nil {
			slog.Default().Error("SQLite shutdown failed", "error", err)
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
	var activeRequests sync.WaitGroup
	// Keep the counter non-zero until shutdown has stopped accepting requests;
	// this makes a handler starting concurrently with Shutdown safe to Add.
	activeRequests.Add(1)
	trackedHandler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		activeRequests.Add(1)
		defer activeRequests.Done()
		gatewayHandler.ServeHTTP(response, request)
	})
	server := &http.Server{
		Addr:           cfg.ListenAddr,
		Handler:        trackedHandler,
		MaxHeaderBytes: 16 * 1024,
	}
	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	select {
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			slog.Default().Error("HTTP server failed", "error", err)
			cleanupStartup(nil, usageObservationWorker, aggregateAccumulator, budgetAccumulator, completionLogger, historyWorker)
			return err
		}
	case <-shutdownContext.Done():
		shutdownErr := shutdownGateway(cfg.ShutdownTimeoutSeconds, server, database, readinessState, &activeRequests,
			usageObservationWorker, aggregateAccumulator, budgetAccumulator, completionLogger, historyWorker)
		if shutdownErr != nil {
			slog.Default().Error("gateway shutdown failed", "error", shutdownErr)
		}
		if err := <-serveErr; err != nil && err != http.ErrServerClosed {
			slog.Default().Error("HTTP server failed", "error", err)
		}
	}
	return nil
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
func shutdownGateway(timeoutSeconds int64, server *http.Server, database *storage.DB, readiness *httpserver.ReadinessState, active *sync.WaitGroup,
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
		active.Done()
		active.Wait()
	}
	if err := usage.Drain(ctx); err != nil {
		slog.Default().Error("usage observation shutdown failed", "error", err)
	}
	if err := token.Shutdown(ctx); err != nil {
		slog.Default().Error("token aggregate shutdown failed", "error", err)
	}
	if err := budget.Shutdown(ctx); err != nil {
		slog.Default().Error("budget aggregate shutdown failed", "error", err)
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
	slog.Default().Info("closing storage", "dropped", dropped)
	if database != nil {
		if err := database.Close(); err != nil {
			drainErr = errors.Join(drainErr, err)
		}
	}
	if drainErr != nil {
		slog.Default().Error("telemetry shutdown failed", "error", drainErr)
	}
	slog.Default().Info("shutdown complete", "dropped", dropped)
	return errors.Join(shutdownErr, drainErr)
}

func keyRecordByID(records []storage.APIKeyRecord, id string) (storage.APIKeyRecord, bool) {
	for _, record := range records {
		if record.ID == id {
			return record, true
		}
	}
	return storage.APIKeyRecord{}, false
}
