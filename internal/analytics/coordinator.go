package analytics

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrCapacityExceeded is returned when the global analytics concurrency limit is reached.
	ErrCapacityExceeded = errors.New("analytics capacity exceeded")
)

type cacheEntry[T any] struct {
	value     T
	createdAt time.Time
}

type flightCall[T any] struct {
	done chan struct{}
	val  T
	err  error
}

// Gate limits concurrent active executions across one or more Coordinators.
type Gate struct {
	mu       sync.Mutex
	capacity int
	active   int
}

// NewGate creates a new concurrency Gate with the specified capacity (defaults to 2).
func NewGate(capacity int) *Gate {
	if capacity <= 0 {
		capacity = 2
	}
	return &Gate{capacity: capacity}
}

// TryAcquire attempts to acquire an execution slot non-blocking.
func (g *Gate) TryAcquire() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active >= g.capacity {
		return false
	}
	g.active++
	return true
}

// Release releases an acquired execution slot.
func (g *Gate) Release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active > 0 {
		g.active--
	}
}

// Active returns the current active acquired slots.
func (g *Gate) Active() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.active
}

// Coordinator provides reusable global concurrency limiting (max 2),
// bounded range caching (<=32 entries, <=15s TTL), and identical-query singleflight.
type Coordinator[T any] struct {
	mu         sync.Mutex
	gate       *Gate
	maxCache   int
	ttl        time.Duration
	cache      map[string]cacheEntry[T]
	cacheOrder []string
	inFlight   map[string]*flightCall[T]
}

// NewCoordinator creates a new analytics Coordinator with a private Gate.
func NewCoordinator[T any](capacity, maxCache int, ttl time.Duration) *Coordinator[T] {
	return NewCoordinatorWithGate[T](NewGate(capacity), maxCache, ttl)
}

// NewCoordinatorWithGate creates a new analytics Coordinator sharing a Gate.
func NewCoordinatorWithGate[T any](gate *Gate, maxCache int, ttl time.Duration) *Coordinator[T] {
	if gate == nil {
		gate = NewGate(2)
	}
	if maxCache <= 0 {
		maxCache = 32
	}
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	return &Coordinator[T]{
		gate:     gate,
		maxCache: maxCache,
		ttl:      ttl,
		cache:    make(map[string]cacheEntry[T]),
		inFlight: make(map[string]*flightCall[T]),
	}
}

// Do executes fn with bounded concurrency and caching.
func (c *Coordinator[T]) Do(ctx context.Context, key string, fn func(ctx context.Context) (T, error)) (T, error) {
	var zero T
	if ctx == nil {
		return zero, errors.New("nil context")
	}

	c.mu.Lock()
	// 1. Check cache
	if entry, ok := c.cache[key]; ok {
		if time.Since(entry.createdAt) <= c.ttl {
			c.mu.Unlock()
			return entry.value, nil
		}
		// Expired
		delete(c.cache, key)
		c.removeKeyFromOrder(key)
	}

	// 2. Singleflight check
	if call, ok := c.inFlight[key]; ok {
		c.mu.Unlock()
		select {
		case <-call.done:
			return call.val, call.err
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}

	// 3. Concurrency check (non-blocking immediate safe 503)
	if !c.gate.TryAcquire() {
		c.mu.Unlock()
		return zero, ErrCapacityExceeded
	}

	call := &flightCall[T]{done: make(chan struct{})}
	c.inFlight[key] = call
	c.mu.Unlock()

	// 4. Run query
	val, err := fn(ctx)

	// 5. Update state
	c.mu.Lock()
	c.gate.Release()
	delete(c.inFlight, key)

	if err == nil && ctx.Err() == nil {
		c.setCache(key, val)
	}

	call.val = val
	call.err = err
	close(call.done)
	c.mu.Unlock()

	return val, err
}

func (c *Coordinator[T]) removeKeyFromOrder(key string) {
	for i, k := range c.cacheOrder {
		if k == key {
			c.cacheOrder = append(c.cacheOrder[:i], c.cacheOrder[i+1:]...)
			break
		}
	}
}

func (c *Coordinator[T]) setCache(key string, val T) {
	c.removeKeyFromOrder(key)
	for len(c.cacheOrder) >= c.maxCache {
		oldest := c.cacheOrder[0]
		c.cacheOrder = c.cacheOrder[1:]
		delete(c.cache, oldest)
	}
	c.cache[key] = cacheEntry[T]{
		value:     val,
		createdAt: time.Now(),
	}
	c.cacheOrder = append(c.cacheOrder, key)
}

// ActiveCount returns current active running queries.
func (c *Coordinator[T]) ActiveCount() int {
	if c.gate == nil {
		return 0
	}
	return c.gate.Active()
}

// CacheLen returns current cached entries count.
func (c *Coordinator[T]) CacheLen() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.cache)
}
