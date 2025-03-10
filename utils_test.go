package query

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

//------Queries------//

type testQueryStruct struct {
}

func (*testQueryStruct) ID() []byte {
	return []byte("UUID")
}

type testQueryEmptyResult struct {
}

func (*testQueryEmptyResult) ID() []byte {
	return []byte("UUID-EMPTY-RESULT")
}

type testQueryError struct {
}

func (*testQueryError) ID() []byte {
	return []byte("UUID-ERROR")
}

type testQueryString string

func (testQueryString) ID() []byte {
	return []byte("UUID")
}

type testQueryUnsupported struct {
}

func (*testQueryUnsupported) ID() []byte {
	return []byte("UUID-UNSUPPORTED")
}

type testCacheQuery struct {
}

func (*testCacheQuery) ID() []byte {
	return []byte("UUID-CACHE")
}

func (*testCacheQuery) CacheKey() []byte {
	return []byte("CACHE-KEY")
}

func (*testCacheQuery) CacheDuration() time.Duration {
	return time.Second
}

type testCacheQuery2 struct {
}

func (*testCacheQuery2) ID() []byte {
	return []byte("UUID-CACHE-2")
}

func (*testCacheQuery2) CacheKey() []byte {
	return []byte("CACHE-KEY-2")
}

func (*testCacheQuery2) CacheDuration() time.Duration {
	return 0
}

type testHandlerOrderQuery struct {
	position  *uint32
	unordered *uint32
}

func (qry *testHandlerOrderQuery) HandlerPosition(position uint32) {
	if position != atomic.LoadUint32(qry.position) {
		atomic.StoreUint32(qry.unordered, 1)
	}
	atomic.AddUint32(qry.position, 1)

}
func (qry *testHandlerOrderQuery) IsUnordered() bool {
	return atomic.LoadUint32(qry.unordered) == 1
}
func (*testHandlerOrderQuery) ID() []byte {
	return []byte("UUID")
}

//------Handlers------//

type testHandler struct {
}

func (hdl *testHandler) Handle(ctx context.Context, qry Query, res *Result) error {
	switch qry.(type) {
	case *testQueryStruct, testQueryString:
		res.Set([]interface{}{"bar"})
		return nil
	case *testQueryEmptyResult:
		res.Done()
		return nil
	}
	return nil
}

type testHandlerWithErrors struct {
}

func (hdl *testHandlerWithErrors) Handle(ctx context.Context, qry Query, res *Result) error {
	switch qry.(type) {
	case *testQueryError:
		return errors.New("query failed")
	}
	return nil
}

type testHandlerOrder struct {
	position uint32
}

func (hdl *testHandlerOrder) Handle(ctx context.Context, qry Query, res *Result) error {
	if qry, listens := qry.(*testHandlerOrderQuery); listens {
		qry.HandlerPosition(hdl.position)
		res.Add("bar")
		return nil
	}
	return nil
}

type testIteratorHandler struct {
}

func (hdl *testIteratorHandler) Handle(ctx context.Context, qry Query, res *IteratorResult) error {
	switch qry.(type) {
	case *testQueryStruct, testQueryString:
		res.Yield("bar")
		res.Done()
		return nil
	}
	return nil
}

type testIteratorHandlerWithErrors struct {
}

func (hdl *testIteratorHandlerWithErrors) Handle(ctx context.Context, qry Query, res *IteratorResult) error {
	switch qry.(type) {
	case *testQueryError:
		return errors.New("query failed")
	}
	return nil
}

type testCacheHandler struct {
}

func (hdl *testCacheHandler) Handle(ctx context.Context, qry Query, res *Result) error {
	switch qry.(type) {
	case *testCacheQuery, *testCacheQuery2:
		// simulate that it took a second to fetch this resource
		// the cache should take over repeated requests for this query, removing the delay
		time.Sleep(time.Second)
		res.Add("bar")
		res.Add("bar")
		return nil
	}
	return nil
}

type testIteratorHandlerOrder struct {
	position uint32
}

func (hdl *testIteratorHandlerOrder) Handle(ctx context.Context, qry Query, res *IteratorResult) error {
	if qry, listens := qry.(*testHandlerOrderQuery); listens {
		qry.HandlerPosition(hdl.position)
		res.Yield("bar")
		return nil
	}
	return nil
}

//------Error Handlers------//

type storeErrorsHandler struct {
	sync.Mutex
	errs map[string]error
}

func (hdl *storeErrorsHandler) Handle(ctx context.Context, qry Query, err error) {
	hdl.Lock()
	hdl.errs[hdl.key(qry)] = err
	hdl.Unlock()
}

func (hdl *storeErrorsHandler) Error(qry Query) error {
	hdl.Lock()
	err, hasError := hdl.errs[hdl.key(qry)]
	hdl.Unlock()
	if hasError {
		return err
	}
	return nil
}

func (hdl *storeErrorsHandler) key(qry Query) string {
	if qry == nil {
		return "nil"
	}
	return string(qry.ID())
}
