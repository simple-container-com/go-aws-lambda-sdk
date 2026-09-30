package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

// bodyAdapter is a minimal HttpAdapter capturing the status code and payload
// written via JSON(). It embeds the interface (nil) and overrides only what
// ReadBody touches, so an unexpected adapter call fails loudly rather than
// silently returning a zero value.
type bodyAdapter struct {
	HttpAdapter
	req      *http.Request
	body     io.Reader
	jsonCode int
	jsonBody any
}

func (a *bodyAdapter) Request() *http.Request   { return a.req }
func (a *bodyAdapter) RequestBody() io.Reader   { return a.body }
func (a *bodyAdapter) JSON(code int, obj any)   { a.jsonCode, a.jsonBody = code, obj }
func (a *bodyAdapter) Context() context.Context { return a.req.Context() }

func newBodyAdapter(body io.Reader) *bodyAdapter {
	return &bodyAdapter{
		req:  httptest.NewRequest(http.MethodPost, "/api/thing", nil),
		body: body,
	}
}

// bodyService is the non-nil Service case. Only Logger() and GetMeta() are
// reachable from ReadBody, so everything else stays nil and panics loudly if
// it is ever called.
type bodyService struct {
	Service
	log logger.Logger
}

func (s *bodyService) Logger() logger.Logger            { return s.log }
func (*bodyService) GetMeta(context.Context) ResultMeta { return ResultMeta{RequestUID: "uid-1"} }
func (*bodyService) IsRequestDebugEnabled() bool        { return true }

type bodyPayload struct {
	Label string `json:"label"`
}

type bodyResult struct {
	OK bool `json:"ok"`
}

// errReader simulates a transport failure part-way through the body.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset by peer") }

// malformedBodies are the shapes a client actually sends by accident: a
// truncated object (connection cut mid-write), a fragment, an empty body, a
// proxy error page, and the right JSON of the wrong shape.
var malformedBodies = map[string]string{
	"truncated object":            `{"label":`,
	"truncated key":               `{"la`,
	"empty body":                  ``,
	"not json at all":             `<html>502 Bad Gateway</html>`,
	"array where object expected": `["a","b"]`,
}

// A malformed body is a CLIENT error. It used to answer 500, which put every
// truncated request into the 5xx rate services alert on.
func TestReadBody_MalformedBody_Returns400(t *testing.T) {
	for name, body := range malformedBodies {
		t.Run(name, func(t *testing.T) {
			for svcName, svc := range map[string]Service{
				// nil is the case that panicked: a handler holding the Service
				// as a field wired after service.New() returns.
				"nil service":    nil,
				"logger present": &bodyService{log: logger.NewLogger()},
				"logger nil":     &bodyService{},
			} {
				t.Run(svcName, func(t *testing.T) {
					c := newBodyAdapter(strings.NewReader(body))

					got, ok := ReadBody[bodyPayload](context.Background(), svc, c)

					assert.False(t, ok, "ok must be false for body %q", body)
					assert.Nil(t, got)
					assert.Equal(t, http.StatusBadRequest, c.jsonCode)
				})
			}
		})
	}
}

// ReadBody must never return (nil, true) — that combination is what let a
// caller dereference a nil pointer.
func TestReadBody_NeverReturnsNilWithOK(t *testing.T) {
	for _, body := range []string{`{"label":`, ``, `{"label":"ok"}`, `null`, `<bad>`} {
		c := newBodyAdapter(strings.NewReader(body))
		got, ok := ReadBody[bodyPayload](context.Background(), nil, c)
		if ok {
			assert.NotNil(t, got, "body %q returned (nil, true) — the contract this fix exists to prevent", body)
		}
	}
}

// The 400 body must be a plain Error: no stack trace, none of the
// aws-lambda-go panic-envelope keys, and not the decoder's internal
// "...to Config" wording (Config is a type almost no route has).
func TestReadBody_ErrorBodyIsPlain(t *testing.T) {
	c := newBodyAdapter(strings.NewReader(`{"label":`))

	_, ok := ReadBody[bodyPayload](context.Background(), nil, c)
	require.False(t, ok)

	raw, err := json.Marshal(c.jsonBody)
	require.NoError(t, err, "response must be JSON-serialisable")

	for _, leak := range []string{"stackTrace", "errorType", "errorMessage", "aws-lambda-go", "goroutine", ".go:", "to Config"} {
		assert.NotContains(t, string(raw), leak)
	}

	var resp Error
	require.NoError(t, json.Unmarshal(raw, &resp))
	assert.Equal(t, "invalid JSON in request body", resp.Message)
}

// A request body can carry a bearer token or a credential blob; the raw body
// must never come back to the client (and, since this change, is not logged
// either — request-debug used to log it verbatim).
func TestReadBody_DoesNotEchoBody(t *testing.T) {
	const secret = "fk-live-must-not-appear"

	for name, svc := range map[string]Service{
		"nil service":           nil,
		"request debug enabled": &bodyService{log: logger.NewLogger()},
	} {
		t.Run(name, func(t *testing.T) {
			c := newBodyAdapter(strings.NewReader(`{"token":"` + secret + `",`))

			_, ok := ReadBody[bodyPayload](context.Background(), svc, c)
			require.False(t, ok)

			raw, _ := json.Marshal(c.jsonBody)
			assert.NotContains(t, string(raw), secret, "response echoes the request body")
		})
	}
}

// A nil body reader is a 400, not a panic in ReadBytes' buf.ReadFrom(nil).
func TestReadBody_NilBodyReader_Returns400(t *testing.T) {
	c := newBodyAdapter(nil)

	got, ok := ReadBody[bodyPayload](context.Background(), nil, c)

	assert.False(t, ok)
	assert.Nil(t, got)
	assert.Equal(t, http.StatusBadRequest, c.jsonCode)
}

// A mid-stream read failure is a 400, not a panic.
func TestReadBody_ReadError_Returns400(t *testing.T) {
	c := newBodyAdapter(errReader{})

	got, ok := ReadBody[bodyPayload](context.Background(), nil, c)

	assert.False(t, ok)
	assert.Nil(t, got)
	assert.Equal(t, http.StatusBadRequest, c.jsonCode)
}

// A nil ctx must not panic on the way to the logger or GetMeta.
func TestReadBody_NilContext_Returns400(t *testing.T) {
	c := newBodyAdapter(strings.NewReader(`{"label":`))

	//nolint:staticcheck // passing a nil ctx is exactly what is under test
	got, ok := ReadBody[bodyPayload](nil, &bodyService{log: logger.NewLogger()}, c)

	assert.False(t, ok)
	assert.Nil(t, got)
	assert.Equal(t, http.StatusBadRequest, c.jsonCode)
}

// The happy path still decodes and writes no response.
func TestReadBody_ValidBody_Decodes(t *testing.T) {
	c := newBodyAdapter(strings.NewReader(`{"label":"thing-1"}`))

	got, ok := ReadBody[bodyPayload](context.Background(), nil, c)

	require.True(t, ok)
	require.NotNil(t, got)
	assert.Equal(t, "thing-1", got.Label)
	assert.Zero(t, c.jsonCode, "wrote a status on the success path")
}

// WithReadBody must never return (nil, true). It used to do exactly that on a
// decode failure: the callback was skipped but ok stayed true, so the caller's
// `res.Meta = ...` panicked.
func TestWithReadBody_MalformedBody_NotOK(t *testing.T) {
	for name, body := range malformedBodies {
		t.Run(name, func(t *testing.T) {
			called := false
			c := newBodyAdapter(strings.NewReader(body))

			got, ok := WithReadBody(context.Background(), nil, c, "do the thing",
				func(*bodyPayload) (*bodyResult, error) {
					called = true
					return &bodyResult{OK: true}, nil
				})

			assert.False(t, ok, "ok must be false so the caller does not dereference got")
			assert.Nil(t, got)
			assert.False(t, called, "callback ran on an undecodable body")
			assert.Equal(t, http.StatusBadRequest, c.jsonCode)
		})
	}
}

// A callback error is the server's fault, not the client's: 500, ok=false, and
// the existing "failed to <action>: <err>" wording is preserved.
func TestWithReadBody_CallbackError_Returns500(t *testing.T) {
	c := newBodyAdapter(strings.NewReader(`{"label":"thing-1"}`))

	got, ok := WithReadBody(context.Background(), &bodyService{log: logger.NewLogger()}, c, "do the thing",
		func(*bodyPayload) (*bodyResult, error) {
			return nil, errors.New("boom")
		})

	assert.False(t, ok)
	assert.Nil(t, got)
	assert.Equal(t, http.StatusInternalServerError, c.jsonCode)

	raw, _ := json.Marshal(c.jsonBody)
	assert.Contains(t, string(raw), "failed to do the thing: boom")
}

// A callback that reports success with no payload leaves the caller holding
// nil just as a failed decode did, so it must not be reported as ok.
func TestWithReadBody_NilResultNoError_IsNotOK(t *testing.T) {
	c := newBodyAdapter(strings.NewReader(`{"label":"thing-1"}`))

	got, ok := WithReadBody(context.Background(), nil, c, "do the thing",
		func(*bodyPayload) (*bodyResult, error) { return nil, nil })

	assert.False(t, ok)
	assert.Nil(t, got)
	assert.Equal(t, http.StatusInternalServerError, c.jsonCode)
}

// The happy path hands back the payload and writes nothing — the caller owns
// the success response.
func TestWithReadBody_HappyPath(t *testing.T) {
	c := newBodyAdapter(strings.NewReader(`{"label":"thing-1"}`))

	var seen string
	got, ok := WithReadBody(context.Background(), nil, c, "do the thing",
		func(cfg *bodyPayload) (*bodyResult, error) {
			seen = cfg.Label
			return &bodyResult{OK: true}, nil
		})

	require.True(t, ok)
	require.NotNil(t, got)
	assert.True(t, got.OK)
	assert.Equal(t, "thing-1", seen)
	assert.Zero(t, c.jsonCode, "wrote a status on the success path")
}

// The caller pattern this whole change exists to protect: the ok-branch
// dereferences the result unconditionally. Pre-fix this panicked on a
// malformed body; it must now simply not enter the branch.
func TestWithReadBody_CallerDereferenceIsSafe(t *testing.T) {
	svc := &bodyService{log: logger.NewLogger()}

	for name, body := range malformedBodies {
		t.Run(name, func(t *testing.T) {
			c := newBodyAdapter(strings.NewReader(body))

			assert.NotPanics(t, func() {
				if res, ok := WithReadBody(context.Background(), svc, c, "do the thing",
					func(*bodyPayload) (*Error, error) { return &Error{}, nil }); ok {
					res.Meta = svc.GetMeta(context.Background()) // the panicking statement
					c.JSON(http.StatusOK, res)
				}
			})
			assert.Equal(t, http.StatusBadRequest, c.jsonCode)
		})
	}
}

// ReadBytes is exported and its nil-reader panic was reachable from any
// adapter that hands back a nil body.
func TestReadBytes_NilReader(t *testing.T) {
	assert.NotPanics(t, func() {
		assert.Empty(t, ReadBytes(nil))
	})
}
