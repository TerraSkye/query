package query

import (
	"context"
	"runtime"
	"sync/atomic"
	"time"
)

const iteratorListenerTimeout = time.Second

// Bus is the only struct exported and required for the query bus usage.
// The Bus should be instantiated using the NewBus function.
type Bus struct {
	iteratorWorkerPoolSize int
	iteratorQueueBuffer    int
	iteratorResultBuffer   int
	initialized            *uint32
	shuttingDown           *uint32
	iteratorWorkers        *uint32
	handlers               []Handler
	iteratorHandlers       []IteratorHandler
	errorHandlers          []ErrorHandler
	cacheAdapters          []CacheAdapter
	iteratorQueryQueue     chan *pendingIteratorQuery
	closed                 chan bool
}

// NewBus instantiates the Bus struct.
// The Initialization of IteratorHandlers is performed separately (InitializeIteratorHandlers function) for dependency injection purposes.
func NewBus() *Bus {
	return &Bus{
		iteratorWorkerPoolSize: runtime.GOMAXPROCS(0),
		iteratorQueueBuffer:    100,
		iteratorResultBuffer:   0,
		initialized:            new(uint32),
		shuttingDown:           new(uint32),
		iteratorWorkers:        new(uint32),
		handlers:               make([]Handler, 0),
		iteratorHandlers:       make([]IteratorHandler, 0),
		errorHandlers:          make([]ErrorHandler, 0),
		cacheAdapters:          []CacheAdapter{NewMemoryCacheAdapter()},
		closed:                 make(chan bool),
	}
}

// Handlers for the regular queries.
func (bus *Bus) Handlers(hdls ...Handler) {
	bus.handlers = hdls
}

// ErrorHandlers may optionally be provided.
// They will receive any error thrown during the querying process.
func (bus *Bus) ErrorHandlers(hdls ...ErrorHandler) {
	bus.errorHandlers = hdls
}

// CacheAdapters may optionally be provided.
// They will be used instead of the default MemoryCacheAdapter.
func (bus *Bus) CacheAdapters(adps ...CacheAdapter) {
	for _, adp := range bus.cacheAdapters {
		adp.Shutdown()
	}
	bus.cacheAdapters = adps
}

// IteratorWorkerPoolSize may optionally be provided to tweak the iteratorWorker pool size for iterator query queue.
// It can only be adjusted *before* the bus is initialized.
// It defaults to the value returned by runtime.GOMAXPROCS(0).
func (bus *Bus) IteratorWorkerPoolSize(workerPoolSize int) {
	if !bus.isInitialized() {
		bus.iteratorWorkerPoolSize = workerPoolSize
	}
}

// IteratorQueueBuffer may optionally be provided to tweak the buffer size of the iterator query queue.
// This value may have high impact on performance depending on the use case.
// It can only be adjusted *before* the bus is initialized.
// It defaults to 100.
func (bus *Bus) IteratorQueueBuffer(buf int) {
	if !bus.isInitialized() {
		bus.iteratorQueueBuffer = buf
	}
}

// IteratorResultBuffer may optionally be provided to tweak the buffer size of the results channel for iterator queries.
// This value may have high impact on performance depending on the use case.
// It defaults to 1.
func (bus *Bus) IteratorResultBuffer(buf int) {
	bus.iteratorResultBuffer = buf
}

// InitializeIteratorHandlers initializes the query bus to support iterator queries.
func (bus *Bus) InitializeIteratorHandlers(hdls ...IteratorHandler) {
	if bus.initialize() {
		bus.iteratorHandlers = hdls
		bus.iteratorQueryQueue = make(chan *pendingIteratorQuery, bus.iteratorQueueBuffer)
		for i := 0; i < bus.iteratorWorkerPoolSize; i++ {
			bus.iteratorWorkerUp()
			go bus.iteratorWorker(bus.iteratorQueryQueue, bus.closed)
		}
	}
}

// Query for a single result or a pre-populated collection.
func (bus *Bus) Query(ctx context.Context, qry Query) (*Result, error) {
	if err := bus.isValid(ctx, qry); err != nil {
		return nil, err
	}

	res, cached := bus.result(ctx, qry)
	if cached {
		return res, nil
	}

	return res, bus.query(ctx, qry, res)
}

// IteratorQuery uses a channel to iterate the results while they are being populated.
// *Iterator queries are not cached*.
func (bus *Bus) IteratorQuery(ctx context.Context, qry Query) (*IteratorResult, error) {
	if err := bus.isIteratorValid(ctx, qry); err != nil {
		return nil, err
	}

	res := newIteratorResult(bus.iteratorResultBuffer)
	bus.enqueueIteratorQuery(ctx, qry, res)
	return res, nil
}

// Shutdown the query bus gracefully.
// *Queries handled while shutting down will be disregarded*.
func (bus *Bus) Shutdown() {
	if atomic.CompareAndSwapUint32(bus.shuttingDown, 0, 1) {
		bus.shutdown()
	}
}

//-----Private Functions------//

func (bus *Bus) initialize() bool {
	return atomic.CompareAndSwapUint32(bus.initialized, 0, 1)
}

func (bus *Bus) isInitialized() bool {
	return atomic.LoadUint32(bus.initialized) == 1
}

func (bus *Bus) isShuttingDown() bool {
	return atomic.LoadUint32(bus.shuttingDown) == 1
}

func (bus *Bus) iteratorWorker(qryQ <-chan *pendingIteratorQuery, closed chan<- bool) {
	for penQry := range qryQ {
		// nil queries are used as signals to break out
		if penQry == nil {
			break
		}

		// wait for a listener
		if penQry.res.waitListener(iteratorListenerTimeout) {
			bus.iteratorQuery(penQry.ctx, penQry.qry, penQry.res)
			penQry.res.close()
			continue
		}

		bus.error(penQry.ctx, penQry.qry, NewErrorQueryTimedOut(penQry.qry))
	}
	closed <- true
}

func (bus *Bus) iteratorQuery(ctx context.Context, qry Query, res *IteratorResult) {
	for _, hdl := range bus.iteratorHandlers {
		if err := hdl.Handle(ctx, qry, res); err != nil {
			bus.error(ctx, qry, err)
			return
		}
		if res.propagationStopped() {
			return
		}
	}
	if !res.isHandled() {
		bus.error(ctx, qry, NewErrorNoQueryHandlersFound(qry))
	}
}

func (bus *Bus) enqueueIteratorQuery(ctx context.Context, qry Query, res *IteratorResult) {
	bus.iteratorQueryQueue <- &pendingIteratorQuery{
		ctx: ctx,
		qry: qry,
		res: res,
	}
}

func (bus *Bus) query(ctx context.Context, qry Query, res *Result) error {
	for _, hdl := range bus.handlers {
		if err := hdl.Handle(ctx, qry, res); err != nil {
			bus.error(ctx, qry, err)
			return err
		}
		if res.propagationStopped() {
			break
		}
	}

	if !res.isHandled() {
		err := NewErrorNoQueryHandlersFound(qry)
		bus.error(ctx, qry, err)
		return err
	}

	bus.handleCache(ctx, qry, res)
	return nil
}

func (bus *Bus) result(ctx context.Context, qry Query) (*Result, bool) {
	if qry, implements := qry.(Cacheable); implements {
		for _, adp := range bus.cacheAdapters {
			if res := adp.Get(ctx, qry); res != nil {
				res.loadedFromCache()
				return res, true
			}
		}
		return newCacheableResult(qry), false
	}
	return newResult(), false
}

func (bus *Bus) handleCache(ctx context.Context, qry Query, res *Result) {
	if qry, implements := qry.(Cacheable); implements && qry.CacheDuration() > 0 {
		at := time.Now()
		res.expires(at.Add(qry.CacheDuration()))
		cached := false
		for _, adp := range bus.cacheAdapters {
			cached = cached || adp.Set(ctx, qry, res)
		}
		if cached {
			res.cached(at)
		}
	}
}

func (bus *Bus) iteratorWorkerUp() {
	atomic.AddUint32(bus.iteratorWorkers, 1)
}

func (bus *Bus) iteratorWorkerDown() {
	atomic.AddUint32(bus.iteratorWorkers, ^uint32(0))
}

func (bus *Bus) shutdown() {
	for atomic.LoadUint32(bus.iteratorWorkers) > 0 {
		bus.iteratorQueryQueue <- nil
		<-bus.closed
		bus.iteratorWorkerDown()
	}
	for _, adp := range bus.cacheAdapters {
		adp.Shutdown()
	}
	atomic.CompareAndSwapUint32(bus.initialized, 1, 0)
	atomic.CompareAndSwapUint32(bus.shuttingDown, 1, 0)
}

func (bus *Bus) isValid(ctx context.Context, qry Query) error {
	var err error
	if qry == nil {
		err = InvalidQueryError
		bus.error(ctx, qry, err)
		return err
	}
	return nil
}

func (bus *Bus) isIteratorValid(ctx context.Context, qry Query) error {
	err := bus.isValid(ctx, qry)
	if err != nil {
		return err
	}
	if !bus.isInitialized() {
		err = BusNotInitializedError
		bus.error(ctx, qry, err)
		return err
	}
	if bus.isShuttingDown() {
		err = BusIsShuttingDownError
		bus.error(ctx, qry, err)
		return err
	}
	return nil
}

func (bus *Bus) error(ctx context.Context, qry Query, err error) {
	for _, errHdl := range bus.errorHandlers {
		errHdl.Handle(ctx, qry, err)
	}
}
