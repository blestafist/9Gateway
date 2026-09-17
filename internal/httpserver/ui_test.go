package httpserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testUIMockFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte("<!DOCTYPE html><html><head><title>Mock App</title></head><body>Root</body></html>"),
		},
		"assets/bundle-12345.js": &fstest.MapFile{
			Data: []byte("console.log('mock bundle');"),
		},
		"assets/style-12345.css": &fstest.MapFile{
			Data: []byte("body { margin: 0; }"),
		},
	}
}

func TestUIRoutes_Redirect(t *testing.T) {
	handler := newUIHandler(testUIMockFS())

	tests := []struct {
		name           string
		method         string
		path           string
		expectedStatus int
		expectedLoc    string
	}{
		{
			name:           "GET /ui redirects to /ui/",
			method:         http.MethodGet,
			path:           "/ui",
			expectedStatus: http.StatusMovedPermanently,
			expectedLoc:    "/ui/",
		},
		{
			name:           "HEAD /ui redirects to /ui/",
			method:         http.MethodHead,
			path:           "/ui",
			expectedStatus: http.StatusMovedPermanently,
			expectedLoc:    "/ui/",
		},
		{
			name:           "GET /ui with query parameters preserves query",
			method:         http.MethodGet,
			path:           "/ui?view=keys&filter=active",
			expectedStatus: http.StatusMovedPermanently,
			expectedLoc:    "/ui/?view=keys&filter=active",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(tc.method, tc.path, nil)
			handler.ServeHTTP(recorder, request)

			if recorder.Code != tc.expectedStatus {
				t.Fatalf("expected status %d, got %d", tc.expectedStatus, recorder.Code)
			}
			loc := recorder.Header().Get("Location")
			if loc != tc.expectedLoc {
				t.Fatalf("expected Location %q, got %q", tc.expectedLoc, loc)
			}
			if tc.method == http.MethodHead && recorder.Body.Len() != 0 {
				t.Fatalf("expected empty body for HEAD, got %d bytes", recorder.Body.Len())
			}
		})
	}
}

func TestUIRoutes_SPAIndexAndFallback(t *testing.T) {
	handler := newUIHandler(testUIMockFS())

	tests := []struct {
		name string
		path string
	}{
		{name: "root UI path", path: "/ui/"},
		{name: "index.html direct path", path: "/ui/index.html"},
		{name: "client route", path: "/ui/example"},
		{name: "nested client route", path: "/ui/example/nested/view"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, tc.path, nil)
			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
			}
			contentType := recorder.Header().Get("Content-Type")
			if !strings.HasPrefix(contentType, "text/html") {
				t.Fatalf("expected text/html Content-Type, got %q", contentType)
			}
			cacheControl := recorder.Header().Get("Cache-Control")
			if cacheControl != "no-cache" {
				t.Fatalf("expected Cache-Control: no-cache, got %q", cacheControl)
			}
			if !strings.Contains(recorder.Body.String(), "<title>Mock App</title>") {
				t.Fatalf("expected SPA document body, got %q", recorder.Body.String())
			}
		})
	}
}

func TestUIRoutes_HashedAssets(t *testing.T) {
	handler := newUIHandler(testUIMockFS())

	t.Run("existing hashed js asset", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/ui/assets/bundle-12345.js", nil)
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
		}
		if cache := recorder.Header().Get("Cache-Control"); cache != "public, max-age=31536000, immutable" {
			t.Fatalf("expected immutable Cache-Control, got %q", cache)
		}
		if ct := recorder.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
			t.Fatalf("expected javascript Content-Type, got %q", ct)
		}
		if !strings.Contains(recorder.Body.String(), "console.log('mock bundle');") {
			t.Fatalf("unexpected asset body: %q", recorder.Body.String())
		}
	})

	t.Run("existing hashed css asset", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/ui/assets/style-12345.css", nil)
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
		}
		if cache := recorder.Header().Get("Cache-Control"); cache != "public, max-age=31536000, immutable" {
			t.Fatalf("expected immutable Cache-Control, got %q", cache)
		}
		if ct := recorder.Header().Get("Content-Type"); !strings.Contains(ct, "text/css") {
			t.Fatalf("expected text/css Content-Type, got %q", ct)
		}
	})

	t.Run("missing hashed asset returns 404 rather than index.html", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/ui/assets/missing-99999.js", nil)
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusNotFound {
			t.Fatalf("expected status %d for missing hashed asset, got %d", http.StatusNotFound, recorder.Code)
		}
		if strings.Contains(recorder.Body.String(), "Mock App") {
			t.Fatalf("missing hashed asset must not return index.html fallback")
		}
	})

	t.Run("missing hashed asset HEAD returns 404", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodHead, "/ui/assets/missing-99999.js", nil)
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusNotFound {
			t.Fatalf("expected status %d for HEAD missing hashed asset, got %d", http.StatusNotFound, recorder.Code)
		}
	})
}

func TestUIRoutes_HEAD(t *testing.T) {
	handler := newUIHandler(testUIMockFS())

	tests := []struct {
		name          string
		path          string
		expectedCache string
		expectedCT    string
	}{
		{
			name:          "HEAD /ui/",
			path:          "/ui/",
			expectedCache: "no-cache",
			expectedCT:    "text/html; charset=utf-8",
		},
		{
			name:          "HEAD /ui/example",
			path:          "/ui/example",
			expectedCache: "no-cache",
			expectedCT:    "text/html; charset=utf-8",
		},
		{
			name:          "HEAD /ui/assets/bundle-12345.js",
			path:          "/ui/assets/bundle-12345.js",
			expectedCache: "public, max-age=31536000, immutable",
			expectedCT:    "text/javascript; charset=utf-8",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodHead, tc.path, nil)
			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
			}
			if recorder.Body.Len() != 0 {
				t.Fatalf("expected empty body for HEAD, got %d bytes", recorder.Body.Len())
			}
			if cache := recorder.Header().Get("Cache-Control"); cache != tc.expectedCache {
				t.Fatalf("expected Cache-Control %q, got %q", tc.expectedCache, cache)
			}
			if ct := recorder.Header().Get("Content-Type"); !strings.Contains(ct, strings.Split(tc.expectedCT, ";")[0]) {
				t.Fatalf("expected Content-Type containing %q, got %q", tc.expectedCT, ct)
			}
		})
	}
}

func TestUIRoutes_UnsupportedMethods(t *testing.T) {
	handler := newUIHandler(testUIMockFS())

	unsupported := []string{
		http.MethodPost,
		http.MethodPut,
		http.MethodDelete,
		http.MethodPatch,
		http.MethodOptions,
	}

	paths := []string{
		"/ui",
		"/ui/",
		"/ui/example",
		"/ui/assets/bundle-12345.js",
	}

	for _, method := range unsupported {
		for _, path := range paths {
			t.Run(method+" "+path, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(method, path, strings.NewReader("sample payload"))
				handler.ServeHTTP(recorder, request)

				if recorder.Code != http.StatusMethodNotAllowed {
					t.Fatalf("expected status 405 Method Not Allowed, got %d", recorder.Code)
				}
				allow := recorder.Header().Get("Allow")
				if allow != "GET, HEAD" {
					t.Fatalf("expected Allow: GET, HEAD, got %q", allow)
				}
			})
		}
	}
}

func TestUIRoutes_EmbeddedFS_EndToEnd(t *testing.T) {
	// Tests against the actual embedded web.Dist() assets compiled by Vite.
	handler := newUIHandler(nil)

	t.Run("GET /ui/ serves real embedded index.html", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/ui/", nil)
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", recorder.Code)
		}
		if recorder.Header().Get("Cache-Control") != "no-cache" {
			t.Fatalf("expected Cache-Control: no-cache, got %q", recorder.Header().Get("Cache-Control"))
		}
		body := recorder.Body.String()
		if !strings.Contains(body, "9Gateway Console") {
			t.Fatalf("expected body to contain '9Gateway Console', got %q", body)
		}
		if !strings.Contains(body, "/ui/assets/") {
			t.Fatalf("expected body to reference /ui/assets/, got %q", body)
		}
	})

	t.Run("GET /ui/example fallback serves real embedded index.html", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/ui/example", nil)
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", recorder.Code)
		}
		if !strings.Contains(recorder.Body.String(), "9Gateway Console") {
			t.Fatalf("expected SPA document on nested route, got %q", recorder.Body.String())
		}
	})
}

func TestUIRoutes_PreserveExistingRoutes(t *testing.T) {
	// Use route with mock proxy to test complete route dispatch
	mockProxy := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Mock-Proxy", "true")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"proxied":true}`)
	})

	router := route(mockProxy)

	t.Run("GET /health is preserved", func(t *testing.T) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("unexpected health response: status %d", rec.Code)
		}
	})

	t.Run("Unknown /v1/* transparent passthrough preserved", func(t *testing.T) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/custom/endpoint", strings.NewReader(`{}`)))
		if rec.Header().Get("X-Mock-Proxy") != "true" {
			t.Fatalf("expected request to pass through to proxy, got status %d", rec.Code)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200 from mock proxy, got %d", rec.Code)
		}
	})

	t.Run("GET /ui redirects to /ui/", func(t *testing.T) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui", nil))
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/ui/" {
			t.Fatalf("expected 301 to /ui/, got status %d, loc %q", rec.Code, rec.Header().Get("Location"))
		}
	})

	t.Run("GET /ui/ serves SPA document", func(t *testing.T) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/", nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "9Gateway Console") {
			t.Fatalf("expected 200 with SPA document, got status %d", rec.Code)
		}
	})

	t.Run("GET /unknown returns 404 JSON gateway error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/unknown", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "not_found") {
			t.Fatalf("expected not_found JSON error, got %q", rec.Body.String())
		}
	})

	t.Run("GET /ready and GET /metrics preserved with readiness and metrics wrappers", func(t *testing.T) {
		readiness := NewReadiness(ReadinessConfig{State: &ReadinessState{}})
		handler := WithReadiness(withMetrics(newGatewayMetrics(), router), readiness)

		// /ready is handled by readiness probe
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))
		if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected /ready probe response (200 or 503), got %d", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"status":`) {
			t.Fatalf("expected readiness JSON response, got %q", rec.Body.String())
		}

		// /metrics returns 200 text/plain
		recMetrics := httptest.NewRecorder()
		handler.ServeHTTP(recMetrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if recMetrics.Code != http.StatusOK {
			t.Fatalf("expected /metrics status 200, got %d", recMetrics.Code)
		}
		if !strings.Contains(recMetrics.Body.String(), "gateway_requests_total") {
			t.Fatalf("expected prometheus metrics body, got %q", recMetrics.Body.String())
		}
	})
}
