package openai

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestInspectRequestBodyFittingBodies(t *testing.T) {
	for _, test := range []struct {
		name  string
		body  string
		limit int64
	}{
		{name: "below limit", body: "hello", limit: 6},
		{name: "exact limit", body: "hello", limit: 5},
		{name: "empty", body: "", limit: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &closeTrackingReader{Reader: strings.NewReader(test.body)}

			inspected, replacement, available, err := InspectRequestBody(source, test.limit)
			if err != nil {
				t.Fatalf("InspectRequestBody error = %v, want nil", err)
			}
			if !available {
				t.Fatal("available = false, want true")
			}
			if string(inspected) != test.body {
				t.Fatalf("inspected = %q, want %q", inspected, test.body)
			}
			assertReaderBytes(t, replacement, []byte(test.body))
			if source.closed != 0 {
				t.Fatalf("input close count = %d, want 0", source.closed)
			}
		})
	}
}

func TestInspectRequestBodyOverLimitPreservesUnreadBody(t *testing.T) {
	const limit = 4
	body := []byte("0123456789")
	source := &fragmentedReader{body: body, fragmentSize: 2}

	inspected, replacement, available, err := InspectRequestBody(source, limit)
	if err != nil {
		t.Fatalf("InspectRequestBody error = %v, want nil", err)
	}
	if available {
		t.Fatal("available = true, want false")
	}
	if inspected != nil {
		t.Fatalf("inspected = %q, want nil for over-limit body", inspected)
	}
	if source.readBytes != limit+1 {
		t.Fatalf("source read bytes = %d, want %d before replacement is read", source.readBytes, limit+1)
	}
	assertReaderBytes(t, replacement, body)
}

func TestInspectRequestBodyFragmentedReaderPreservesBody(t *testing.T) {
	body := []byte("fragmented request body")
	source := &fragmentedReader{body: body, fragmentSize: 1}

	inspected, replacement, available, err := InspectRequestBody(source, int64(len(body)))
	if err != nil {
		t.Fatalf("InspectRequestBody error = %v, want nil", err)
	}
	if !available {
		t.Fatal("available = false, want true")
	}
	if !bytes.Equal(inspected, body) {
		t.Fatalf("inspected = %q, want %q", inspected, body)
	}
	assertReaderBytes(t, replacement, body)
}

func TestInspectRequestBodyReadErrorPreservesConsumedBytes(t *testing.T) {
	readErr := errors.New("source failed")
	source := &errorReader{
		body:      []byte("partial body"),
		failAfter: 3,
		err:       readErr,
	}

	_, replacement, available, err := InspectRequestBody(source, 32)
	if !errors.Is(err, readErr) {
		t.Fatalf("InspectRequestBody error = %v, want errors.Is(..., readErr)", err)
	}
	if available {
		t.Fatal("available = true, want false")
	}
	got, replacementErr := io.ReadAll(replacement)
	if !bytes.Equal(got, source.body[:source.failAfter]) {
		t.Fatalf("replacement bytes = %q, want consumed prefix %q", got, source.body[:source.failAfter])
	}
	if !errors.Is(replacementErr, readErr) {
		t.Fatalf("replacement error = %v, want errors.Is(..., readErr)", replacementErr)
	}
}

func TestInspectRequestBodyOverLimitReadIsBounded(t *testing.T) {
	const limit = 8
	body := bytes.Repeat([]byte{'x'}, 1024)
	source := &countingReader{Reader: bytes.NewReader(body)}

	_, replacement, available, err := InspectRequestBody(source, limit)
	if err != nil {
		t.Fatalf("InspectRequestBody error = %v, want nil", err)
	}
	if available {
		t.Fatal("available = true, want false")
	}
	if source.readBytes > limit+1 {
		t.Fatalf("source was drained: read %d bytes, want at most %d", source.readBytes, limit+1)
	}
	assertReaderBytes(t, replacement, body)
}

func TestInspectRequestBodyRejectsNonPositiveLimit(t *testing.T) {
	for _, test := range []struct {
		name  string
		limit int64
	}{
		{name: "zero", limit: 0},
		{name: "negative", limit: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, replacement, available, err := InspectRequestBody(strings.NewReader("body"), test.limit)
			if !errors.Is(err, ErrInvalidRequestBodyLimit) {
				t.Fatalf("InspectRequestBody error = %v, want errors.Is(..., ErrInvalidRequestBodyLimit)", err)
			}
			if replacement != nil || available {
				t.Fatalf("replacement = %v, available = %t; want nil, false", replacement, available)
			}
		})
	}
}

func assertReaderBytes(t *testing.T, reader io.Reader, want []byte) {
	t.Helper()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read replacement: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("replacement bytes = %q, want %q", got, want)
	}
}

type closeTrackingReader struct {
	io.Reader
	closed int
}

func (reader *closeTrackingReader) Close() error {
	reader.closed++
	return nil
}

type countingReader struct {
	io.Reader
	readBytes int
}

func (reader *countingReader) Read(p []byte) (int, error) {
	n, err := reader.Reader.Read(p)
	reader.readBytes += n
	return n, err
}

type fragmentedReader struct {
	body         []byte
	position     int
	fragmentSize int
	readBytes    int
}

func (reader *fragmentedReader) Read(p []byte) (int, error) {
	if reader.position == len(reader.body) {
		return 0, io.EOF
	}
	n := reader.fragmentSize
	if n > len(p) {
		n = len(p)
	}
	if remaining := len(reader.body) - reader.position; n > remaining {
		n = remaining
	}
	copy(p, reader.body[reader.position:reader.position+n])
	reader.position += n
	reader.readBytes += n
	return n, nil
}

type errorReader struct {
	body      []byte
	position  int
	failAfter int
	err       error
}

func (reader *errorReader) Read(p []byte) (int, error) {
	if reader.position >= reader.failAfter {
		return 0, reader.err
	}
	n := reader.failAfter - reader.position
	if n > len(p) {
		n = len(p)
	}
	copy(p, reader.body[reader.position:reader.position+n])
	reader.position += n
	if reader.position == reader.failAfter {
		return n, reader.err
	}
	return n, nil
}
