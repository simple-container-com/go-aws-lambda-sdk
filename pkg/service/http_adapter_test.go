package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	echo "github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

// A body-less POST used to reach handlers with Request.Body == nil, because the
// Lambda event->request adapters do not honour net/http's http.NoBody
// invariant. Every unguarded json.NewDecoder(c.Request().Body).Decode(&v) in
// every dependent service then panicked on a nil interface. These tests pin the
// normalisation at the SDK boundary so those call sites need no change.
//
// nilBodyRequest reproduces the pre-fix condition: http.NewRequest with a nil
// io.Reader leaves Body nil, exactly as its-felix's getBody does for an empty
// payload.
func nilBodyRequest(t *testing.T) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/thing", nil)
	req.Body = nil
	require.Nil(t, req.Body, "precondition: the request must start with a nil Body")
	return req
}

// decodeHandler is the shape that used to panic: decode straight off
// Request().Body, no nil guard, no ReadBody helper.
func decodeHandler(seen *map[string]any, decodeErr *error) HttpAdapterHandler {
	return func(c HttpAdapter) error {
		var payload map[string]any
		err := json.NewDecoder(c.Request().Body).Decode(&payload)
		*decodeErr = err
		*seen = payload
		c.JSON(http.StatusOK, map[string]any{"ok": true})
		return nil
	}
}

func TestGinAdapter_NilBody_NormalizedToNoBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var (
		seen      map[string]any
		decodeErr error
	)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = nilBodyRequest(t)

	require.NotPanics(t, func() {
		GinAdapter(decodeHandler(&seen, &decodeErr), logger.NewLogger(), false)(c)
	})

	assert.Equal(t, http.NoBody, c.Request.Body, "nil Body must be normalized to http.NoBody")
	// http.NoBody reads as an immediately-empty stream, so the decoder reports
	// EOF — the same thing net/http would have produced — instead of panicking.
	assert.ErrorIs(t, decodeErr, io.EOF)
	assert.Nil(t, seen)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestEchoAdapter_NilBody_NormalizedToNoBody(t *testing.T) {
	var (
		seen      map[string]any
		decodeErr error
	)
	e := echo.New()
	rec := httptest.NewRecorder()
	c := e.NewContext(nilBodyRequest(t), rec)

	require.NotPanics(t, func() {
		require.NoError(t, EchoAdapter(decodeHandler(&seen, &decodeErr), logger.NewLogger(), false)(c))
	})

	assert.Equal(t, http.NoBody, c.Request().Body)
	assert.ErrorIs(t, decodeErr, io.EOF)
	assert.Nil(t, seen)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// The fix must not disturb the common case: a non-empty body has to arrive
// byte-for-byte, unread and unwrapped.
func TestGinAdapter_NonEmptyBody_PreservedByteForByte(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const raw = `{"label":"hello","nested":{"n":1},"unicode":"привет"}`

	var got string
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/thing", strings.NewReader(raw))

	GinAdapter(func(a HttpAdapter) error {
		b, err := io.ReadAll(a.Request().Body)
		require.NoError(t, err)
		got = string(b)
		return nil
	}, logger.NewLogger(), false)(c)

	assert.Equal(t, raw, got)
	assert.NotEqual(t, http.NoBody, c.Request.Body, "a non-empty body must not be replaced")
}

func TestEchoAdapter_NonEmptyBody_PreservedByteForByte(t *testing.T) {
	const raw = `{"label":"hello","nested":{"n":1},"unicode":"привет"}`

	var got string
	e := echo.New()
	c := e.NewContext(
		httptest.NewRequest(http.MethodPost, "/api/thing", strings.NewReader(raw)),
		httptest.NewRecorder(),
	)

	require.NoError(t, EchoAdapter(func(a HttpAdapter) error {
		b, err := io.ReadAll(a.Request().Body)
		require.NoError(t, err)
		got = string(b)
		return nil
	}, logger.NewLogger(), false)(c))

	assert.Equal(t, raw, got)
	assert.NotEqual(t, http.NoBody, c.Request().Body)
}

// ReadBody answers 400 on a nil body (helpers_test.go covers that). Once the
// adapter normalises, a body-less POST reaching ReadBody is an empty body
// rather than a missing one — still a 400, but via the JSON path, which is what
// a client sending no body should see.
func TestGinAdapter_NilBody_ReadBodyStillRejects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = nilBodyRequest(t)

	svc := &bodyService{log: logger.NewLogger()}
	var ok bool
	require.NotPanics(t, func() {
		GinAdapter(func(a HttpAdapter) error {
			_, ok = ReadBody[bodyPayload](a.Context(), svc, a)
			return nil
		}, logger.NewLogger(), false)(c)
	})
	assert.False(t, ok, "an empty body is still not a valid payload")
}

// The router-level middleware is the part that covers everything GinAdapter
// does not: consumer middleware registered via Use, swagger, NoRoute.
func TestNormalizeBodyGinMiddleware_CoversPlainHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(normalizeBodyGinMiddleware())

	var body io.ReadCloser
	engine.POST("/api/thing", func(c *gin.Context) {
		body = c.Request.Body
		c.Status(http.StatusNoContent)
	})

	req := nilBodyRequest(t)
	rec := httptest.NewRecorder()
	require.NotPanics(t, func() { engine.ServeHTTP(rec, req) })

	assert.Equal(t, http.NoBody, body)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestNormalizeBodyEchoMiddleware_CoversPlainHandlers(t *testing.T) {
	e := echo.New()
	e.Pre(normalizeBodyEchoMiddleware())

	var body io.ReadCloser
	e.POST("/api/thing", func(c echo.Context) error {
		body = c.Request().Body
		return c.NoContent(http.StatusNoContent)
	})

	rec := httptest.NewRecorder()
	require.NotPanics(t, func() { e.ServeHTTP(rec, nilBodyRequest(t)) })

	assert.Equal(t, http.NoBody, body)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

// WithVanillaHandler and the Yandex trigger unwrapper both bypass the routers,
// so they are wrapped with withNormalizedBody instead.
func TestWithNormalizedBody(t *testing.T) {
	var body io.ReadCloser
	h := withNormalizedBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = r.Body
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	require.NotPanics(t, func() { h.ServeHTTP(rec, nilBodyRequest(t)) })
	assert.Equal(t, http.NoBody, body)

	// Non-empty passes through untouched.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/thing", strings.NewReader("payload")))
	assert.NotEqual(t, http.NoBody, body, "a non-empty body must not be replaced")
	b, err := io.ReadAll(body)
	require.NoError(t, err)
	assert.Equal(t, "payload", string(b))
}

func TestNormalizeRequestBody_NilRequestIsSafe(t *testing.T) {
	assert.NotPanics(t, func() { normalizeRequestBody(nil) })
}
