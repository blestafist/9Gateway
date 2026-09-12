package openai

import (
	"bytes"
	"testing"
	"time"
)

func TestT128MeaningfulTimingIgnoresMetadataHeartbeatsAndKeepsUsageOnlyTerminal(t *testing.T) {
	input := "data: {\"id\":\"x\",\"model\":\"m\"}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n" +
		"data: {}\n\n" +
		"data: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\n"
	now := time.Unix(10, 0)
	var times []time.Time
	result, err := ObserveStreamWithTiming(bytes.NewBufferString(input), 4096, nil, func(int64) time.Time {
		at := now.Add(time.Duration(len(times)) * time.Second)
		times = append(times, at)
		return at
	})
	if err != nil || len(times) != 2 {
		t.Fatalf("observation = (%+v, %v), timing callbacks = %d; want content and usage-only terminal", result, err, len(times))
	}
}

func TestT128AggregationTimingInvalidatesCheckpointClockRegression(t *testing.T) {
	input := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"b\"}}]}\n\n"
	calls := 0
	result, err := AggregateSSEToJSONWithTiming(bytes.NewBufferString(input), 4096, 1024, func() time.Time {
		calls++
		if calls == 1 {
			return time.Unix(20, 0)
		}
		return time.Unix(19, 0)
	})
	if calls != 2 {
		t.Fatalf("meaningful callbacks = %d, want 2", calls)
	}
	if err != nil || !result.LastMeaningfulAt.IsZero() {
		t.Fatalf("aggregation timing = (%v, %v), want unknown after regression", result.LastMeaningfulAt, err)
	}
}

func TestT128MeaningfulTimingIgnoresEmptyAndMetadataOnlyEvents(t *testing.T) {
	input := "data: {\"id\":\"x\",\"model\":\"m\"}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"}}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning\":\"think\"}}]}\n\n"
	calls := 0
	_, err := ObserveStreamWithTiming(bytes.NewBufferString(input), 4096, nil, func(int64) time.Time {
		calls++
		return time.Unix(int64(calls), 0)
	})
	if err != nil || calls != 1 {
		t.Fatalf("observation error/callbacks = (%v, %d), want nil/1", err, calls)
	}
}
