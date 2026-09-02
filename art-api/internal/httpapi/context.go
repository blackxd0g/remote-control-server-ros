package httpapi

import (
	"context"

	"github.com/art-rustdesk/platform/art-api/internal/auth"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
)

type authContextKey struct{}

type Principal struct {
	User    domain.User
	Session domain.Session
	Claims  auth.Claims
}

func withPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, authContextKey{}, principal)
}

func principalFrom(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(authContextKey{}).(Principal)
	return principal, ok
}
