package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

// TestGetMeta_BareContext is the regression: GetMeta must not panic on a
// context that never passed through requestUIDMiddleware.
//
// Only New installs that middleware, so a host composing its own router
// (Local Forge mounts several services on one echo instance via
// EchoRouter) always arrives here with an empty context. The old
// `.(time.Time)` assertion panicked, net/http dropped the connection, and
// callers saw an unexplained EOF in place of the error the handler was
// reporting — live-caught 2026-11-20 on the embedded aigateway's
// POST /v1/generations, where it masked every 4xx/5xx of that service.
func TestGetMeta_BareContext(t *testing.T) {
	svc := &service{logger: logger.NewLogger(), lambdaSize: 512, lambdaCostPerMbPerMillisecond: 0.0001}

	var meta ResultMeta
	require.NotPanics(t, func() { meta = svc.GetMeta(context.Background()) })

	assert.Empty(t, meta.RequestUID)
	assert.True(t, meta.RequestStartedAt.IsZero())
	assert.False(t, meta.IsAuthorized)
	// Zero, not "time since the zero instant": an absent start time must
	// not report a ~2000-year request or a cost derived from it.
	assert.Zero(t, meta.RequestTime)
	assert.Zero(t, meta.Cost)
	assert.False(t, meta.RequestFinishedAt.IsZero())
}

// TestGetMeta_PopulatedContext — the middleware-served path still reports
// real values.
func TestGetMeta_PopulatedContext(t *testing.T) {
	log := logger.NewLogger()
	svc := &service{logger: log, lambdaSize: 512, lambdaCostPerMbPerMillisecond: 0.0001}

	startedAt := time.Now().Add(-250 * time.Millisecond)
	ctx := log.WithValue(context.Background(), RequestStartedKey, startedAt)
	ctx = log.WithValue(ctx, RequestUIDKey, "uid-1")
	ctx = log.WithValue(ctx, IsAuthorizedKey, true)

	meta := svc.GetMeta(ctx)
	assert.Equal(t, "uid-1", meta.RequestUID)
	assert.Equal(t, startedAt, meta.RequestStartedAt)
	assert.True(t, meta.IsAuthorized)
	assert.GreaterOrEqual(t, meta.RequestTime, 250*time.Millisecond)
	assert.Positive(t, meta.Cost)
}

// TestGetMeta_WrongTypes — a middleware (or a host) that stored the wrong
// type under a known key degrades to the zero value instead of panicking.
func TestGetMeta_WrongTypes(t *testing.T) {
	log := logger.NewLogger()
	svc := &service{logger: log}

	ctx := log.WithValue(context.Background(), RequestStartedKey, "not-a-time")
	ctx = log.WithValue(ctx, RequestUIDKey, 42)
	ctx = log.WithValue(ctx, IsAuthorizedKey, "yes")

	var meta ResultMeta
	require.NotPanics(t, func() { meta = svc.GetMeta(ctx) })
	assert.Empty(t, meta.RequestUID)
	assert.True(t, meta.RequestStartedAt.IsZero())
	assert.False(t, meta.IsAuthorized)
}
