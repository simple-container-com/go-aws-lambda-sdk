package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	echoadapter "github.com/its-felix/aws-lambda-go-http-adapter/adapter"
	echohandler "github.com/its-felix/aws-lambda-go-http-adapter/handler"
	echo "github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aws/aws-lambda-go/events"
)

// The tests in http_adapter_test.go construct the nil Body directly. This one
// proves the root cause instead: it drives a real empty-body Lambda Function URL
// event through the real its-felix adapter the SDK wires up for
// RESPONSE_STREAM services, so it fails if the upstream behaviour ever changes
// AND it demonstrates the normalisation works on a genuinely adapter-built
// request rather than a hand-made one.
//
// This is the production path for any service deployed with
// lambdaInvokeMode: RESPONSE_STREAM.
func emptyBodyFunctionURLEvent(method string) events.LambdaFunctionURLRequest {
	return events.LambdaFunctionURLRequest{
		Version: "2.0",
		RawPath: "/api/thing",
		Body:    "", // what API Gateway / Function URL sends for a body-less POST
		Headers: map[string]string{"content-type": "application/json"},
		RequestContext: events.LambdaFunctionURLRequestContext{
			HTTP: events.LambdaFunctionURLRequestContextHTTPDescription{
				Method: method,
				Path:   "/api/thing",
			},
		},
	}
}

// TestItsFelixAdapter_EmptyBodyEvent_ProducesNilBody documents the upstream
// behaviour this fix exists for. If this ever fails, its-felix has started
// honouring the http.NoBody invariant itself and the SDK's normalisation has
// become redundant (harmless, but removable).
func TestItsFelixAdapter_EmptyBodyEvent_ProducesNilBody(t *testing.T) {
	var gotNilBody bool
	e := echo.New()
	// No normalisation installed here: this asserts the raw upstream behaviour.
	e.POST("/api/thing", func(c echo.Context) error {
		gotNilBody = c.Request().Body == nil
		return c.NoContent(http.StatusNoContent)
	})

	start := echohandler.NewFunctionURLHandler(echoadapter.NewEchoAdapter(e))
	_, err := start(context.Background(), emptyBodyFunctionURLEvent(http.MethodPost))
	require.NoError(t, err)

	assert.True(t, gotNilBody,
		"upstream its-felix is expected to hand over a nil Body for an empty payload; "+
			"if this fails the SDK's normalisation is no longer needed")
}

// TestEchoRouter_EmptyBodyEvent_DecodesWithoutPanic is the regression test that
// matters: the same event, through the same adapter, but with the SDK's
// normalisation installed the way service.New installs it — and a handler that
// decodes straight off Request().Body with no nil guard, exactly like the
// dependent services' call sites.
func TestEchoRouter_EmptyBodyEvent_DecodesWithoutPanic(t *testing.T) {
	var (
		decodeErr error
		status    int
	)
	e := echo.New()
	e.Pre(normalizeBodyEchoMiddleware())
	e.POST("/api/thing", func(c echo.Context) error {
		// The unguarded shape that panicked fleet-wide.
		var payload map[string]any
		decodeErr = json.NewDecoder(c.Request().Body).Decode(&payload)
		return c.NoContent(http.StatusNoContent)
	})

	start := echohandler.NewFunctionURLHandler(echoadapter.NewEchoAdapter(e))

	var resp events.LambdaFunctionURLResponse
	var err error
	require.NotPanics(t, func() {
		resp, err = start(context.Background(), emptyBodyFunctionURLEvent(http.MethodPost))
	}, "a body-less POST must not panic the handler")
	require.NoError(t, err)
	status = resp.StatusCode

	// io.EOF is what net/http would have produced for an empty body: the
	// handler's own error handling decides what to do, instead of the process
	// dying with a nil-pointer panic.
	assert.ErrorIs(t, decodeErr, io.EOF)
	assert.Equal(t, http.StatusNoContent, status)
}

// A non-empty event body still arrives intact through the same path.
func TestEchoRouter_NonEmptyBodyEvent_Preserved(t *testing.T) {
	const raw = `{"label":"hello"}`

	var got string
	e := echo.New()
	e.Pre(normalizeBodyEchoMiddleware())
	e.POST("/api/thing", func(c echo.Context) error {
		b, err := io.ReadAll(c.Request().Body)
		require.NoError(t, err)
		got = string(b)
		return c.NoContent(http.StatusNoContent)
	})

	evt := emptyBodyFunctionURLEvent(http.MethodPost)
	evt.Body = raw

	start := echohandler.NewFunctionURLHandler(echoadapter.NewEchoAdapter(e))
	_, err := start(context.Background(), evt)
	require.NoError(t, err)

	assert.Equal(t, raw, got)
}
