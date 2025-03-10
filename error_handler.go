package query

import "context"

// ErrorHandler must be implemented for a type to qualify as an error handler.
type ErrorHandler interface {
	Handle(ctx context.Context, qry Query, err error)
}
