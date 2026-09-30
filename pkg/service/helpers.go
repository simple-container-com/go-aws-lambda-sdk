package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/pkg/errors"
)

// WithReadBody decodes the JSON request body into T, runs callback, and hands
// back a payload the caller can dereference.
//
// ok == true guarantees a non-nil result. Every failure returns (nil, false)
// with the response already written — a malformed body is a 400 (see
// ReadBody), a callback error is a 500, and so is a callback that reports
// success with no payload, which would leave the caller holding nil just the
// same. Callers MUST write nothing on the false branch:
//
//	if res, ok := service.WithReadBody(ctx, s, c, "do the thing", cb); ok {
//	    res.Meta = s.GetMeta(ctx)
//	    c.JSON(http.StatusOK, res)
//	}
//	return nil
//
// This function used to return (nil, TRUE) when the decode failed: the
// callback was skipped but ok stayed true, so the `res.Meta = ...` above
// dereferenced nil and panicked. Live-confirmed 2026-09-30 on a Forge service
// — POST with a truncated body panicked in exactly that statement.
func WithReadBody[T any, R any](ctx context.Context, s Service, c HttpAdapter, action string, callback func(cfg *T) (*R, error)) (*R, bool) {
	ctx = orBackground(ctx)

	model, ok := ReadBody[T](ctx, s, c)
	if !ok {
		// ReadBody has already written the 400.
		return nil, false
	}

	res, err := callback(model)
	if err != nil {
		respondError(ctx, s, c, http.StatusInternalServerError, fmt.Sprintf("failed to %s: %v", action, err), err)
		return nil, false
	}
	if res == nil {
		respondError(ctx, s, c, http.StatusInternalServerError, fmt.Sprintf("failed to %s: no result", action),
			errors.Errorf("callback for %q returned no result and no error", action))
		return nil, false
	}

	return res, true
}

// ReadBody decodes the JSON request body into T.
//
// On any failure it writes an HTTP 400 with a plain message and returns
// (nil, false); it never returns (nil, true). Callers MUST return immediately
// when ok is false — the response has already been written.
//
// Three defects this function used to have, all live-confirmed 2026-09-30
// across the Forge fleet:
//
//  1. It answered **500** for what is unambiguously a client error, so a
//     truncated body from any caller showed up as a server fault in the 5xx
//     rate services alert on.
//  2. It echoed the decoder's internal wording back to the client ("failed to
//     unmarshal request body to Config" — Config is a type that exists on
//     almost no route), and under request-debug it logged the raw body, which
//     is how a request body carrying credentials could reach CloudWatch. The
//     body is no longer logged at all; the decode error alone is.
//  3. It dereferenced s unconditionally on the error path. Where a service is
//     assigned onto a handler struct *after* service.New() returns, s can be
//     nil — the nil deref panicked, and on Lambda the runtime serialised the
//     panic as **HTTP 200 with a Go stack trace** in the body. s may now be
//     nil; it is used only for logging and meta.
func ReadBody[T any](ctx context.Context, s Service, c HttpAdapter) (*T, bool) {
	ctx = orBackground(ctx)

	var result T

	body := c.RequestBody()
	if body == nil {
		respondError(ctx, s, c, http.StatusBadRequest, "request body is required", nil)
		return nil, false
	}

	raw, err := io.ReadAll(body)
	if err != nil {
		respondError(ctx, s, c, http.StatusBadRequest, "failed to read request body", err)
		return nil, false
	}

	if err := json.Unmarshal(raw, &result); err != nil {
		respondError(ctx, s, c, http.StatusBadRequest, "invalid JSON in request body", err)
		return nil, false
	}

	return &result, true
}

// respondError logs cause server-side and writes message to the client. The
// message is a fixed string chosen at the call site: a decoder error can quote
// the offending bytes, and a request body may carry credentials.
func respondError(ctx context.Context, s Service, c HttpAdapter, code int, message string, cause error) {
	if s != nil && cause != nil {
		if log := s.Logger(); log != nil {
			log.Errorf(ctx, "%s: %v", message, cause)
		}
	}
	c.JSON(code, Error{
		Message: message,
		Meta:    metaOrEmpty(ctx, s),
	})
}

func metaOrEmpty(ctx context.Context, s Service) ResultMeta {
	if s == nil {
		return ResultMeta{}
	}
	return s.GetMeta(ctx)
}

func orBackground(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}
