package auth

import "context"

type reqLogKey struct{}

// reqLog is a mutable, request-scoped record that middleware fills in as the
// request is processed (e.g. with the authenticated user, once known).
type reqLog struct {
	User   string
	UserID int64
}

// WithRequestLog attaches an empty request-log record to the context. The
// access-log middleware installs this at the top of the chain so downstream
// auth middleware can record who the request belongs to.
func WithRequestLog(ctx context.Context) context.Context {
	return context.WithValue(ctx, reqLogKey{}, &reqLog{})
}

// RequestLogUser returns the username recorded for the request ("" if none).
func RequestLogUser(ctx context.Context) (string, int64) {
	if rl, ok := ctx.Value(reqLogKey{}).(*reqLog); ok {
		return rl.User, rl.UserID
	}
	return "", 0
}

func setRequestLogUser(ctx context.Context, user string, id int64) {
	if rl, ok := ctx.Value(reqLogKey{}).(*reqLog); ok {
		rl.User = user
		rl.UserID = id
	}
}
