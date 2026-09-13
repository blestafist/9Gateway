package httpserver

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pestit/9gateway/internal/config"
	"github.com/pestit/9gateway/internal/observability"
	"github.com/pestit/9gateway/internal/storage"
)

const defaultHistoryQueueCapacity = config.DefaultTelemetryQueueCapacity

// HistoryPersistenceJob is the bounded handoff to the history writer. The
// worker copies body bytes at submission, so the value queued by the worker is
// immutable and contains no request, context, headers, policy, or limiter
// ownership.
type HistoryPersistenceJob struct {
	Record CompletionRecord
	Bodies []observability.BodySnapshot
}

// NewHistoryPersistenceJob makes a defensive copy of the optional body
// snapshots. It is also useful to callers that already have a final record and
// want the ownership boundary to be explicit.
func NewHistoryPersistenceJob(record CompletionRecord, bodies ...observability.BodySnapshot) HistoryPersistenceJob {
	return cloneHistoryJob(HistoryPersistenceJob{Record: record, Bodies: bodies})
}

// HistoryPersistenceWorkerOptions configures one process-owned history writer.
// Retention pass settings are injectable for tests; zero values select the
// fixed production bounds.
type HistoryPersistenceWorkerOptions struct {
	Repository             *storage.RequestHistoryRepository
	Capacity               int
	RequestRetention       time.Duration
	BodyRetention          time.Duration
	Clock                  func() time.Time
	Now                    func() time.Time
	RetentionEveryJobs     uint64
	MaxBodyRowsPerPass     int
	MaxMetadataRowsPerPass int
}

// HistoryPersistenceStats contains bounded scalar worker counters.
type HistoryPersistenceStats struct {
	Accepted  uint64
	Processed uint64
	Persisted uint64
	Failed    uint64
	Dropped   uint64
}

// HistoryPersistenceWorker persists detailed history best-effort. It owns one
// bounded queue and one worker goroutine; it never performs synchronous SQL in
// Submit.
type HistoryPersistenceWorker struct {
	repository       *storage.RequestHistoryRepository
	queue            chan HistoryPersistenceJob
	stop             chan struct{}
	done             chan struct{}
	workerContext    context.Context
	workerCancel     context.CancelFunc
	requestRetention time.Duration
	bodyRetention    time.Duration
	now              func() time.Time
	every            uint64
	bodyLimit        int
	metadataLimit    int

	mu        sync.Mutex
	accepting bool
	stopOnce  sync.Once
	abort     atomic.Bool

	accepted  atomic.Uint64
	processed atomic.Uint64
	persisted atomic.Uint64
	failed    atomic.Uint64
	dropped   atomic.Uint64
}

// NewHistoryPersistenceWorker starts one bounded history writer.
func NewHistoryPersistenceWorker(options HistoryPersistenceWorkerOptions) *HistoryPersistenceWorker {
	if options.Capacity <= 0 {
		options.Capacity = defaultHistoryQueueCapacity
	}
	if options.RequestRetention <= 0 {
		options.RequestRetention = time.Duration(config.DefaultRequestRetentionSeconds) * time.Second
	}
	if options.BodyRetention <= 0 {
		options.BodyRetention = time.Duration(config.DefaultBodyRetentionSeconds) * time.Second
	}
	if options.RetentionEveryJobs == 0 {
		options.RetentionEveryJobs = config.RetentionPassEveryJobs
	}
	if options.MaxBodyRowsPerPass <= 0 {
		options.MaxBodyRowsPerPass = config.RetentionMaxBodyRowsPerPass
	}
	if options.MaxMetadataRowsPerPass <= 0 {
		options.MaxMetadataRowsPerPass = config.RetentionMaxMetadataRowsPerPass
	}
	if options.Clock == nil {
		options.Clock = options.Now
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	ctx, cancel := context.WithCancel(context.Background())
	worker := &HistoryPersistenceWorker{
		repository:       options.Repository,
		queue:            make(chan HistoryPersistenceJob, options.Capacity),
		stop:             make(chan struct{}),
		done:             make(chan struct{}),
		workerContext:    ctx,
		workerCancel:     cancel,
		requestRetention: options.RequestRetention,
		bodyRetention:    options.BodyRetention,
		now:              options.Clock,
		every:            options.RetentionEveryJobs,
		bodyLimit:        options.MaxBodyRowsPerPass,
		metadataLimit:    options.MaxMetadataRowsPerPass,
		accepting:        true,
	}
	go worker.run()
	return worker
}

func (worker *HistoryPersistenceWorker) run() {
	defer close(worker.done)
	worker.retentionPass()
	for {
		select {
		case job := <-worker.queue:
			worker.process(job)
		case <-worker.stop:
			worker.drain()
			return
		}
	}
}

func (worker *HistoryPersistenceWorker) drain() {
	for {
		select {
		case job := <-worker.queue:
			if worker.abort.Load() {
				worker.drop(job)
				continue
			}
			worker.process(job)
		default:
			return
		}
	}
}

func (worker *HistoryPersistenceWorker) process(job HistoryPersistenceJob) {
	worker.processed.Add(1)
	err := storage.ErrHistoryRepositoryUnavailable
	if worker.repository != nil {
		err = worker.repository.Persist(worker.workerContext, historyRecord(job.Record), job.Bodies)
	}
	if err != nil {
		worker.failed.Add(1)
	} else {
		worker.persisted.Add(1)
	}
	clearHistoryJob(&job)
	if worker.processed.Load()%worker.every == 0 {
		worker.mu.Lock()
		accepting := worker.accepting
		worker.mu.Unlock()
		if accepting && !worker.abort.Load() {
			worker.retentionPass()
		}
	}
}

func (worker *HistoryPersistenceWorker) retentionPass() {
	worker.mu.Lock()
	accepting := worker.accepting
	worker.mu.Unlock()
	if !accepting || worker.abort.Load() || worker.repository == nil {
		if worker.repository == nil && accepting {
			worker.failed.Add(1)
		}
		return
	}
	now := worker.now().UTC()
	bodyErr := error(nil)
	metadataErr := error(nil)
	_, bodyErr = worker.repository.DeleteBodiesBefore(worker.workerContext, now.Add(-worker.bodyRetention), worker.bodyLimit)
	_, metadataErr = worker.repository.DeleteMetadataBefore(worker.workerContext, now.Add(-worker.requestRetention), worker.metadataLimit)
	if bodyErr != nil || metadataErr != nil {
		worker.failed.Add(1)
	}
}

// Submit hands off one immutable job without waiting. A false result means
// the bounded detailed telemetry was dropped; it never falls back to SQL.
func (worker *HistoryPersistenceWorker) Submit(job HistoryPersistenceJob) bool {
	if worker == nil {
		clearHistoryJob(&job)
		return false
	}
	job = cloneHistoryJob(job)
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if !worker.accepting {
		worker.drop(job)
		return false
	}
	select {
	case worker.queue <- job:
		worker.accepted.Add(1)
		return true
	default:
		worker.drop(job)
		return false
	}
}

// SubmitRecord is the convenient record-oriented form of Submit.
func (worker *HistoryPersistenceWorker) SubmitRecord(record CompletionRecord, bodies ...observability.BodySnapshot) bool {
	return worker.Submit(NewHistoryPersistenceJob(record, bodies...))
}

// Enqueue is the conventional nonblocking spelling for Submit.
func (worker *HistoryPersistenceWorker) Enqueue(job HistoryPersistenceJob) bool {
	return worker.Submit(job)
}

func (worker *HistoryPersistenceWorker) drop(job HistoryPersistenceJob) {
	clearHistoryJob(&job)
	worker.dropped.Add(1)
}

// Stats returns only bounded scalar counters.
func (worker *HistoryPersistenceWorker) Stats() HistoryPersistenceStats {
	if worker == nil {
		return HistoryPersistenceStats{}
	}
	return HistoryPersistenceStats{
		Accepted: worker.accepted.Load(), Processed: worker.processed.Load(),
		Persisted: worker.persisted.Load(), Failed: worker.failed.Load(), Dropped: worker.dropped.Load(),
	}
}

func (worker *HistoryPersistenceWorker) Accepted() uint64 {
	return worker.Stats().Accepted
}

func (worker *HistoryPersistenceWorker) Processed() uint64 {
	return worker.Stats().Processed
}

func (worker *HistoryPersistenceWorker) Persisted() uint64 {
	return worker.Stats().Persisted
}

func (worker *HistoryPersistenceWorker) Failed() uint64 {
	return worker.Stats().Failed
}

func (worker *HistoryPersistenceWorker) Dropped() uint64 {
	return worker.Stats().Dropped
}

// Shutdown stops admission, drains queued jobs while the caller's context
// permits, and drops any remainder at deadline. It never closes SQLite.
func (worker *HistoryPersistenceWorker) Shutdown(ctx context.Context) error {
	if worker == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	worker.mu.Lock()
	worker.accepting = false
	worker.mu.Unlock()
	worker.stopOnce.Do(func() { close(worker.stop) })
	select {
	case <-worker.done:
		worker.workerCancel()
		return nil
	case <-ctx.Done():
		worker.abort.Store(true)
		worker.workerCancel()
		worker.mu.Lock()
		for {
			select {
			case job := <-worker.queue:
				worker.drop(job)
			default:
				worker.mu.Unlock()
				return ctx.Err()
			}
		}
	}
}

func cloneHistoryJob(job HistoryPersistenceJob) HistoryPersistenceJob {
	job.Bodies = append([]observability.BodySnapshot(nil), job.Bodies...)
	for index := range job.Bodies {
		job.Bodies[index].Bytes = append([]byte(nil), job.Bodies[index].Bytes...)
	}
	return job
}

func clearHistoryJob(job *HistoryPersistenceJob) {
	if job == nil {
		return
	}
	for index := range job.Bodies {
		job.Bodies[index].Bytes = nil
	}
	job.Bodies = nil
}

func historyRecord(record CompletionRecord) storage.HistoryRecord {
	textMode := func(mode string) string {
		if mode == "unknown" {
			return ""
		}
		return mode
	}
	optional := func(value interface{ Value() (int64, bool) }) storage.OptionalInt64 {
		if number, ok := value.Value(); ok {
			return storage.KnownInt64(number)
		}
		return storage.OptionalInt64{}
	}
	status := func(value OptionalStatus) storage.OptionalInt64 {
		if number, ok := value.Value(); ok {
			return storage.KnownInt64(int64(number))
		}
		return storage.OptionalInt64{}
	}
	return storage.HistoryRecord{
		RequestID: string(record.RequestID), APIKeyID: record.KeyID, KeyName: record.KeyName,
		Method: record.Method, Path: record.Path, Route: textMode(record.Route.String()), Model: record.Model,
		RequestedMode: textMode(record.RequestedMode.String()), UpstreamMode: textMode(string(record.UpstreamMode)), DeliveredMode: textMode(string(record.DeliveredMode)),
		DownstreamStatus: status(record.DownstreamStatus), UpstreamStatus: status(record.UpstreamStatus),
		TerminalOutcome: textMode(string(record.Terminal.Outcome)), UpstreamStarted: record.Terminal.UpstreamStarted,
		ErrorCode: textMode(record.ErrorCode.String()), ClientBytes: optional(record.ClientBytes), UpstreamBytes: optional(record.UpstreamBytes), DeliveredBytes: optional(record.DeliveredBytes),
		InputTokens: optional(record.Usage.Input()), OutputTokens: optional(record.Usage.Output()), TotalTokens: optional(record.Usage.Total()), CachedInputTokens: optional(record.Usage.CachedInput()), ReasoningOutputTokens: optional(record.Usage.ReasoningOutput()),
		CostMicros: optionalMoney(record.Cost), StartedAt: optional(record.Timing.StartedAt), UpstreamStartedAt: optional(record.Timing.UpstreamStartedAt), UpstreamHeadersAt: optional(record.Timing.UpstreamHeadersAt), FirstByteAt: optional(record.Timing.FirstByteAt), FinishedAt: optional(record.Timing.FinishedAt),
		TotalMicros: optional(record.Timing.Total), TimeToUpstreamHeadersMicros: optional(record.Timing.TimeToUpstreamHeaders), TimeToFirstByteMicros: optional(record.Timing.TimeToFirstByte), StreamCloseDelayMicros: optional(record.Timing.StreamCloseDelay),
	}
}

func optionalMoney(value interface{ Micros() (int64, bool) }) storage.OptionalInt64 {
	if number, ok := value.Micros(); ok {
		return storage.KnownInt64(number)
	}
	return storage.OptionalInt64{}
}
