package query

import "context"

// Handler must be implemented for a type to qualify as a query handler.
type Handler interface {
	Handle(ctx context.Context, qry Query, res *Result) error
}
