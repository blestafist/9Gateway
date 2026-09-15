package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/transport"
)

func TestNonStreamingResponseDeliversFirstByteBeforeUpstreamEOF(t *testing.T) {
	const first = "first opaque fragment"
	const rest = " and the response continues"
	upstream := newStreamingUpstream(t, streamingUpstreamScript{
		contentType:    "application/octet-stream",
		fragments:      []string{first, rest},
		flushEach:      true,
		waitAfterFirst: true,
	})
	gateway := httptest.NewServer(newTransportHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	response, err := http.Get(gateway.URL + "/v1/opaque")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	firstBytes := make([]byte, len(first))
	readDone := make(chan error, 1)
	go func() {
		_, readErr := io.ReadFull(response.Body, firstBytes)
		readDone <- readErr
	}()
	select {
	case readErr := <-readDone:
		if readErr != nil {
			t.Fatalf("read first response fragment: %v", readErr)
		}
	case <-time.After(time.Second):
		t.Fatal("non-SSE response was buffered until upstream EOF")
	}
	if string(firstBytes) != first {
		t.Fatalf("first response fragment = %q, want %q", firstBytes, first)
	}
	upstream.release()
	remaining, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read remaining response: %v", err)
	}
	if string(remaining) != rest {
		t.Fatalf("remaining response = %q, want %q", remaining, rest)
	}
}

func TestNonStreamingResponseLimitForwardsExactlyLimitAndNoMore(t *testing.T) {
	for _, test := range []struct {
		name       string
		body       string
		wantBody   string
		wantErr    error
		contentLen string
	}{
		{name: "exact limit", body: "12345", wantBody: "12345"},
		{name: "limit plus one", body: "123456", wantBody: "12345", wantErr: errUpstreamResponseTooLarge},
		{name: "slow limit plus one", body: "123456", wantBody: "12345", wantErr: errUpstreamResponseTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := io.Reader(bytes.NewBufferString(test.body))
			if test.name == "slow limit plus one" {
				body = &reviewChunkReader{chunks: [][]byte{[]byte("123"), []byte("456")}}
			}
			upstream := &http.Response{
				StatusCode: http.StatusPartialContent,
				Header:     http.Header{"Content-Type": []string{"application/octet-stream"}},
				Body:       io.NopCloser(body),
			}
			writer := httptest.NewRecorder()
			err := copyBoundedNonStreamingResponseWithLimit(writer, upstream, 5)
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("copy response error = %v", err)
				}
			} else if err != test.wantErr {
				t.Fatalf("copy response error = %v, want %v", err, test.wantErr)
			}
			if got := writer.Body.String(); got != test.wantBody {
				t.Fatalf("forwarded body = %q, want %q", got, test.wantBody)
			}
			if writer.Body.Len() > 5 {
				t.Fatal("forwarded a byte beyond response limit")
			}
			if writer.Code != http.StatusPartialContent {
				t.Fatalf("status = %d, want %d", writer.Code, http.StatusPartialContent)
			}
		})
	}
}

func TestSSEEventLimitRecognizesCROnlyAndMixedDelimitersAcrossReads(t *testing.T) {
	for _, test := range []struct {
		name   string
		chunks [][]byte
		want   string
	}{
		{
			name:   "CR-only split at delimiter",
			chunks: [][]byte{[]byte("a\r"), []byte("\r"), []byte("bcdefg")},
			want:   "a\r\rbcdef",
		},
		{
			name:   "mixed CR LF split at delimiter",
			chunks: [][]byte{[]byte("a\r"), []byte("\n"), []byte("\n"), []byte("bcdefg")},
			want:   "a\r\n\nbcdef",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &sseEventLimitReader{source: &reviewChunkReader{chunks: test.chunks}, limit: 5}
			got, err := io.ReadAll(reader)
			if err != nil && err != errSSEEventTooLarge {
				t.Fatalf("read error = %v, want SSE event limit", err)
			}
			if err != errSSEEventTooLarge {
				t.Fatalf("read error = %v, want SSE event limit", err)
			}
			if string(got) != test.want {
				t.Fatalf("forwarded SSE bytes = %q, want %q", got, test.want)
			}
		})
	}
}

type reviewChunkReader struct {
	chunks [][]byte
	index  int
	remain []byte
}

func (reader *reviewChunkReader) Read(destination []byte) (int, error) {
	if len(reader.remain) == 0 && reader.index >= len(reader.chunks) {
		return 0, io.EOF
	}
	if len(reader.remain) == 0 {
		reader.remain = reader.chunks[reader.index]
		reader.index++
	}
	chunk := reader.remain
	if len(chunk) > len(destination) {
		chunk = chunk[:len(destination)]
	}
	reader.remain = reader.remain[len(chunk):]
	return copy(destination, chunk), nil
}
