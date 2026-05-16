package app

import (
	"context"
	"testing"
)

func TestUserIdentityContext(t *testing.T) {
	ctx := context.Background()

	ctx = WithUserIdentity(ctx, "user-123", true)

	if got := UserIDFromContext(ctx); got != "user-123" {
		t.Errorf("UserIDFromContext = %q, want %q", got, "user-123")
	}
	if got := TrustModeFromContext(ctx); !got {
		t.Error("TrustModeFromContext = false, want true")
	}
}

func TestUserIdentityContextDefaults(t *testing.T) {
	ctx := context.Background()

	if got := UserIDFromContext(ctx); got != "" {
		t.Errorf("UserIDFromContext on empty ctx = %q, want empty", got)
	}
	if got := TrustModeFromContext(ctx); got {
		t.Error("TrustModeFromContext on empty ctx = true, want false")
	}
}
