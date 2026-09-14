// Command mock-upstream is a deliberately small OpenAI-compatible upstream for
// the local compose walkthrough. It is not intended to model a provider.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type chatRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		if err := healthcheck(); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}

	server := &http.Server{Addr: ":8081", Handler: newHandler(os.Getenv("UPSTREAM_API_KEY"))}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func healthcheck() error {
	client := http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://127.0.0.1:8081/ready")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("mock upstream is not ready: HTTP %d", response.StatusCode)
	}
	return nil
}

func newHandler(expectedAPIKey string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/ready", func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v1/chat/completions", func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			response.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if expectedAPIKey != "" && request.Header.Get("Authorization") != "Bearer "+expectedAPIKey {
			http.Error(response, `{"error":{"message":"invalid upstream API key"}}`, http.StatusUnauthorized)
			return
		}
		var input chatRequest
		if err := json.NewDecoder(io.LimitReader(request.Body, 1<<20)).Decode(&input); err != nil {
			http.Error(response, `{"error":{"message":"invalid JSON"}}`, http.StatusBadRequest)
			return
		}
		if input.Model == "" {
			input.Model = "mock-model"
		}
		if input.Stream {
			writeStream(response, input.Model)
			return
		}
		writeJSON(response, completion(input.Model, "Hello from the local mock upstream."))
	})
	return mux
}

func completion(model, content string) map[string]any {
	return map[string]any{
		"id":      "chatcmpl-local-mock",
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]string{"role": "assistant", "content": content},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{"prompt_tokens": 8, "completion_tokens": 7, "total_tokens": 15},
	}
}

func writeJSON(response http.ResponseWriter, value any) {
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(value)
}

func writeStream(response http.ResponseWriter, model string) {
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.WriteHeader(http.StatusOK)
	flusher, _ := response.(http.Flusher)
	for _, event := range []map[string]any{
		{"id": "chatcmpl-local-mock", "object": "chat.completion.chunk", "model": model, "choices": []map[string]any{{"index": 0, "delta": map[string]string{"role": "assistant", "content": "Hello"}}}},
		{"id": "chatcmpl-local-mock", "object": "chat.completion.chunk", "model": model, "choices": []map[string]any{{"index": 0, "delta": map[string]string{"content": " from the local mock upstream."}, "finish_reason": "stop"}}},
	} {
		payload, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(response, "data: %s\n\n", payload)
		if flusher != nil {
			flusher.Flush()
		}
	}
	_, _ = fmt.Fprint(response, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}
