package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/config"
	"github.com/pestit/9gateway/internal/httpserver"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
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
	database, err := storage.Open(context.Background(), cfg.SQLitePath)
	if err != nil {
		return err
	}
	defer func() {
		if err := database.Close(); err != nil {
			log.Printf("SQLite shutdown: %v", err)
		}
	}()
	keyRepository := storage.NewAPIKeyRepository(database)
	keyRecords, err := keyRepository.List(context.Background())
	if err != nil {
		return err
	}
	tokenLimiter := limiter.NewTokenLimiter(nil)
	usageRepository := storage.NewUsageBucketRepository(database)
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
	processContext, processCancel := context.WithCancel(context.Background())
	defer processCancel()
	aggregateAccumulator := storage.NewUsageAggregateAccumulatorWithContext(processContext, usageRepository)
	tokenLimiter.SetCommittedDeltaSink(aggregateAccumulator.Sink)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := aggregateAccumulator.Shutdown(ctx); err != nil {
			log.Printf("token aggregate shutdown: %v", err)
		}
	}()

	upstreamClient := transport.NewClient()
	completionLogger := httpserver.NewCompletionLogger(slog.Default(), 0)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := completionLogger.Shutdown(ctx); err != nil {
			log.Printf("completion logger shutdown: %v", err)
		}
	}()
	usageObservationWorker := httpserver.NewUsageObservationWorker(httpserver.UsageObservationWorkerOptions{})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := usageObservationWorker.Shutdown(ctx); err != nil {
			log.Printf("usage observation worker shutdown: %v", err)
		}
	}()

	gatewayHandler, err := httpserver.NewHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorker(upstreamClient, cfg.UpstreamBaseURL, cfg.UpstreamAPIKey, cfg.AdminCredential, cfg.AuthPepper, keyRepository, nil, nil, completionLogger, tokenLimiter, httpserver.TokenAdmissionConfig{
		MaxInspectedRequestBytes:   cfg.Tokenizer.MaxInspectedRequestBytes,
		FallbackUnknownInputTokens: cfg.Tokenizer.FallbackUnknownInputTokens,
		FallbackMaxOutputTokens:    cfg.Tokenizer.FallbackMaxOutputTokens,
	}, usageObservationWorker, auth.TokenMode(cfg.Tokenizer.Mode))
	if err != nil {
		return err
	}
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
		Addr:    cfg.ListenAddr,
		Handler: trackedHandler,
	}
	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	select {
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server: %v", err)
		}
	case <-shutdownContext.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		shutdownErr := server.Shutdown(shutdown)
		cancel()
		if shutdownErr != nil {
			log.Printf("HTTP server shutdown: %v", shutdownErr)
			// Shutdown stops accepting work but may leave handlers running when
			// its deadline expires. Force-close those handlers before allowing
			// owned resources, including the completion logger, to exit.
			if err := server.Close(); err != nil {
				log.Printf("HTTP server close: %v", err)
			}
		}
		activeRequests.Done()
		// The force-close above cancels handlers that outlive graceful shutdown;
		// await their completion before the deferred logger and database cleanup.
		activeRequests.Wait()
		if err := <-serveErr; err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server: %v", err)
		}
	}
	return nil
}

func keyRecordByID(records []storage.APIKeyRecord, id string) (storage.APIKeyRecord, bool) {
	for _, record := range records {
		if record.ID == id {
			return record, true
		}
	}
	return storage.APIKeyRecord{}, false
}
