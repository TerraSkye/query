package query

import "context"

// IteratorHandler must be implemented for a type to qualify as an iterator query handler.
type IteratorHandler interface {
	Handle(ctx context.Context, qry Query, res *IteratorResult) error
}
