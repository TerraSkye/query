package query

import "context"

type pendingIteratorQuery struct {
	ctx context.Context
	qry Query
	res *IteratorResult
}
