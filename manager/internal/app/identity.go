package app

import "context"

type contextKey string

const ctxKeyUserID contextKey = "userID"
const ctxKeyTrustMode contextKey = "trustMode"

func WithUserIdentity(ctx context.Context, userID string, trustMode bool) context.Context {
	ctx = context.WithValue(ctx, ctxKeyUserID, userID)
	ctx = context.WithValue(ctx, ctxKeyTrustMode, trustMode)
	return ctx
}

func UserIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyUserID).(string)
	return v
}

func TrustModeFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeyTrustMode).(bool)
	return v
}
