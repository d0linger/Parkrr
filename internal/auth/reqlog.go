package auth

import "context"

type reqLogKey struct{}

// reqLog is a mutable, request-scoped record that middleware fills in as the
// request is processed (e.g. with the authenticated user, once known).
type reqLog struct {
	User      string
	UserID    int64
	RequestID string
	Err       error
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

// SetRequestID records the per-request id so handlers can correlate their own logs
// and the request logger can print it (finding OPS-02).
func SetRequestID(ctx context.Context, id string) {
	if rl, ok := ctx.Value(reqLogKey{}).(*reqLog); ok {
		rl.RequestID = id
	}
}

// RequestID returns the id recorded for the request ("" if none).
func RequestID(ctx context.Context) string {
	if rl, ok := ctx.Value(reqLogKey{}).(*reqLog); ok {
		return rl.RequestID
	}
	return ""
}

// SetRequestError stashes the underlying cause of a 5xx so the request logger can emit
// it centrally, instead of every handler dropping err on the floor (finding OPS-01).
func SetRequestError(ctx context.Context, err error) {
	if rl, ok := ctx.Value(reqLogKey{}).(*reqLog); ok {
		rl.Err = err
	}
}

// RequestError returns the stashed 5xx cause (nil if none).
func RequestError(ctx context.Context) error {
	if rl, ok := ctx.Value(reqLogKey{}).(*reqLog); ok {
		return rl.Err
	}
	return nil
}
