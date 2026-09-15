package httpserver

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pestit/9gateway/internal/config"
	"github.com/pestit/9gateway/internal/observability"
	"github.com/pestit/9gateway/internal/storage"
)

const defaultHistoryQueueCapacity = config.DefaultTelemetryQueueCapacity

// HistoryPersistenceJob is the bounded handoff to the history writer. Bodies
// are immutable, singly-owned buffers: the creator transfers ownership to
// Submit, and must not inspect or mutate them after that call. Submit either
// queues that exact job or clears its bodies on rejection; the worker clears
// them after processing. Recorders are finalized recorder ownership transfers;
// the worker materializes snapshots asynchronously off the HTTP path. The value
// contains no request, context, headers, policy, or limiter ownership.
type HistoryPersistenceJob struct {
	Record    CompletionRecord
	Bodies    []observability.BodySnapshot
	Recorders []*observability.BodyRecorder
}

// NewHistoryPersistenceJob packages a final record and transfers ownership of
// the supplied immutable snapshots. Callers must not reuse the snapshot byte
// buffers after passing the resulting job to Submit.
func NewHistoryPersistenceJob(record CompletionRecord, bodies ...observability.BodySnapshot) HistoryPersistenceJob {
	return HistoryPersistenceJob{Record: record, Bodies: bodies}
}

const productionHistoryRowsPerPass = 1000

type historyRepository interface {
	Persist(context.Context, storage.HistoryRecord, []observability.BodySnapshot) error
	DeleteBodiesBefore(context.Context, time.Time, int) (int64, error)
	DeleteMetadataBefore(context.Context, time.Time, int) (int64, error)
}

// HistoryPersistenceWorkerOptions configures one process-owned history writer.
// Retention pass settings are injectable for tests; zero values select the
// fixed production bounds.
type HistoryPersistenceWorkerOptions struct {
	Repository             historyRepository
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
	Accepted        uint64
	Processed       uint64
	Persisted       uint64
	PersistFailed   uint64
	RetentionFailed uint64
	Dropped         uint64
}

// HistoryPersistenceWorker persists detailed history best-effort. It owns one
// bounded queue and one worker goroutine; it never performs synchronous SQL in
// Submit.
type HistoryPersistenceWorker struct {
	repository       historyRepository
	queue            chan HistoryPersistenceJob
	stop             chan struct{}
	done             chan struct{}
	startupDone      chan struct{}
	workerContext    context.Context
	workerCancel     context.CancelFunc
	requestRetention time.Duration
	bodyRetention    time.Duration
	now              func() time.Time
	every            uint64
	bodyLimit        int
	metadataLimit    int

	mu         sync.Mutex
	accepting  bool
	stopOnce   sync.Once
	startupErr error
	abort      atomic.Bool

	accepted        atomic.Uint64
	processed       atomic.Uint64
	persisted       atomic.Uint64
	persistFailed   atomic.Uint64
	retentionFailed atomic.Uint64
	dropped         atomic.Uint64
	metrics         atomic.Pointer[gatewayMetrics]
	metricsQueueID  uint64
	metricsMu       sync.Mutex
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
	if options.MaxBodyRowsPerPass > productionHistoryRowsPerPass {
		options.MaxBodyRowsPerPass = productionHistoryRowsPerPass
	}
	if options.MaxMetadataRowsPerPass <= 0 {
		options.MaxMetadataRowsPerPass = config.RetentionMaxMetadataRowsPerPass
	}
	if options.MaxMetadataRowsPerPass > productionHistoryRowsPerPass {
		options.MaxMetadataRowsPerPass = productionHistoryRowsPerPass
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
		startupDone:      make(chan struct{}),
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

func (worker *HistoryPersistenceWorker) metricsQueueSource() func() int {
	return func() int { return len(worker.queue) }
}

func (worker *HistoryPersistenceWorker) setMetrics(metrics *gatewayMetrics) {
	worker.metricsMu.Lock()
	defer worker.metricsMu.Unlock()
	previous := worker.metrics.Load()
	if previous == metrics && (metrics == nil || worker.metricsQueueID != 0) {
		return
	}
	if previous != nil && previous != metrics {
		previous.unregisterQueue(worker.metricsQueueID)
	}
	worker.metrics.Store(metrics)
	if metrics != nil {
		worker.metricsQueueID = metrics.registerQueue(worker.metricsQueueSource())
	}
}

func (worker *HistoryPersistenceWorker) run() {
	defer close(worker.done)
	startupErr := worker.retentionPass()
	worker.mu.Lock()
	worker.startupErr = startupErr
	close(worker.startupDone)
	worker.mu.Unlock()
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
	defer func() {
		if recovered := recover(); recovered != nil {
			worker.persistFailed.Add(1)
			if metrics := worker.metrics.Load(); metrics != nil {
				metrics.telemetryResult("failed")
			}
		}
	}()
	defer clearHistoryJob(&job)

	worker.processed.Add(1)

	// Materialize snapshots from recorders asynchronously, off the HTTP path.
	var bodies []observability.BodySnapshot
	if len(job.Recorders) > 0 {
		bodies = make([]observability.BodySnapshot, 0, len(job.Recorders))
		for _, recorder := range job.Recorders {
			if recorder != nil {
				bodies = append(bodies, recorder.Snapshot())
			}
		}
	} else {
		bodies = job.Bodies
	}

	err := storage.ErrHistoryRepositoryUnavailable
	if worker.repository != nil {
		err = worker.repository.Persist(worker.workerContext, historyRecord(job.Record), bodies)
	}
	if err != nil {
		worker.persistFailed.Add(1)
		if metrics := worker.metrics.Load(); metrics != nil {
			metrics.telemetryResult("failed")
		}
	} else {
		worker.persisted.Add(1)
		if metrics := worker.metrics.Load(); metrics != nil {
			metrics.telemetryResult("persisted")
		}
	}

	if worker.processed.Load()%worker.every == 0 {
		worker.mu.Lock()
		accepting := worker.accepting
		worker.mu.Unlock()
		if accepting && !worker.abort.Load() {
			worker.retentionPass()
		}
	}
}

func (worker *HistoryPersistenceWorker) retentionPass() error {
	worker.mu.Lock()
	accepting := worker.accepting
	worker.mu.Unlock()
	if !accepting || worker.abort.Load() || worker.repository == nil {
		if worker.repository == nil && accepting {
			worker.retentionFailed.Add(1)
			return storage.ErrHistoryRepositoryUnavailable
		}
		return nil
	}
	// SQLite history timestamps are stored in microseconds. Truncate before
	// deriving retention cutoffs; passing wall-clock nanoseconds violates the
	// repository's exact cutoff contract and made the startup pass fail on real
	// clocks, leaving the first valid request vulnerable to concurrent cleanup.
	now := worker.now().UTC().Truncate(time.Microsecond)
	bodyErr := error(nil)
	metadataErr := error(nil)
	_, bodyErr = worker.repository.DeleteBodiesBefore(worker.workerContext, now.Add(-worker.bodyRetention), worker.bodyLimit)
	_, metadataErr = worker.repository.DeleteMetadataBefore(worker.workerContext, now.Add(-worker.requestRetention), worker.metadataLimit)
	if bodyErr != nil || metadataErr != nil {
		worker.retentionFailed.Add(1)
	}
	return errors.Join(bodyErr, metadataErr)
}

// Submit takes ownership of one immutable job without waiting. A false result
// means the bounded detailed telemetry was dropped; it never falls back to SQL.
func (worker *HistoryPersistenceWorker) Submit(job HistoryPersistenceJob) bool {
	if worker == nil {
		clearHistoryJob(&job)
		return false
	}
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
	if metrics := worker.metrics.Load(); metrics != nil {
		metrics.telemetryResult("dropped")
	}
}

// Stats returns only bounded scalar counters.
func (worker *HistoryPersistenceWorker) Stats() HistoryPersistenceStats {
	if worker == nil {
		return HistoryPersistenceStats{}
	}
	return HistoryPersistenceStats{
		Accepted: worker.accepted.Load(), Processed: worker.processed.Load(),
		Persisted: worker.persisted.Load(), PersistFailed: worker.persistFailed.Load(),
		RetentionFailed: worker.retentionFailed.Load(), Dropped: worker.dropped.Load(),
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

func (worker *HistoryPersistenceWorker) PersistFailed() uint64 {
	return worker.Stats().PersistFailed
}

func (worker *HistoryPersistenceWorker) RetentionFailed() uint64 {
	return worker.Stats().RetentionFailed
}

func (worker *HistoryPersistenceWorker) Dropped() uint64 {
	return worker.Stats().Dropped
}

// Pending reports the bounded history queue depth for shutdown diagnostics.
func (worker *HistoryPersistenceWorker) Pending() int {
	if worker == nil {
		return 0
	}
	return len(worker.queue)
}

// Done returns a channel that is closed only after the history worker has
// stopped issuing repository calls. It is a storage ownership barrier for the
// process lifecycle owner; callers must not close SQLite before it is closed.
func (worker *HistoryPersistenceWorker) Done() <-chan struct{} {
	if worker == nil {
		return nil
	}
	return worker.done
}

// WaitReady waits until the initial retention pass has completed. Owners that
// open SQLite concurrently with this worker must wait before admitting writes;
// otherwise the startup retention DELETE can contend with the first request's
// history transaction and make an otherwise valid persistence fail.
func (worker *HistoryPersistenceWorker) WaitReady(ctx context.Context) error {
	if worker == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-worker.startupDone:
		if err := ctx.Err(); err != nil {
			return err
		}
		worker.mu.Lock()
		startupErr := worker.startupErr
		worker.mu.Unlock()
		return startupErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Shutdown stops admission, drains queued jobs while the caller's context
// permits, and drops any remainder at deadline. It never closes SQLite and
// returns after the worker exits when possible. On deadline it cancels the
// worker context, drops queued jobs, and returns immediately; the process owner
// must retain SQLite until the worker has actually exited.
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

func clearHistoryJob(job *HistoryPersistenceJob) {
	if job == nil {
		return
	}
	for index := range job.Bodies {
		job.Bodies[index].Bytes = nil
	}
	job.Bodies = nil
	job.Recorders = nil
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
