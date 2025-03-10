package query

import "context"

// CacheAdapter must be implemented for a type to qualify as a cache adapter.
type CacheAdapter interface {
	Set(ctx context.Context, qry Cacheable, res *Result) bool
	Get(ctx context.Context, qry Cacheable) *Result
	Expire(ctx context.Context, qry Cacheable)
	Shutdown()
}
