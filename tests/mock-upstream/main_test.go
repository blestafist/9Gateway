package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerReturnsOpenAICompatibleJSON(t *testing.T) {
	server := httptest.NewServer(newHandler("upstream-secret"))
	defer server.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"demo","stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer upstream-secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("status/content type = %d/%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	var body struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Model != "demo" || len(body.Choices) != 1 || body.Choices[0].Message.Content == "" {
		t.Fatalf("unexpected completion: %+v", body)
	}
}

func TestHandlerReturnsTransparentSSE(t *testing.T) {
	server := httptest.NewServer(newHandler(""))
	defer server.Close()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"demo","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status/content type = %d/%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	scanner := bufio.NewScanner(response.Body)
	var events []string
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "data: ") {
			events = append(events, strings.TrimPrefix(scanner.Text(), "data: "))
		}
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if len(events) != 3 || events[len(events)-1] != "[DONE]" {
		t.Fatalf("SSE events = %v", events)
	}
}

func TestHandlerRejectsWrongUpstreamCredential(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://mock.test/v1/chat/completions", strings.NewReader(`{"model":"demo"}`))
	response := httptest.NewRecorder()
	newHandler("expected").ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
