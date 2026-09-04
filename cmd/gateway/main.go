package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/pestit/9gateway/internal/config"
	"github.com/pestit/9gateway/internal/httpserver"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

func main() {
	configPath := flag.String("config", "", "path to the gateway YAML configuration")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	database, err := storage.Open(context.Background(), cfg.SQLitePath)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			log.Printf("SQLite shutdown: %v", err)
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

	gatewayHandler := httpserver.NewHandlerWithCompletionLogger(upstreamClient, cfg.UpstreamBaseURL, cfg.UpstreamAPIKey, completionLogger)
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
}
