package service

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

// cleanupSchedulePayload is the schedule request forge-storage's hourly cleanup
// job actually carries in client.yaml. It is reproduced verbatim (minus the
// resolved secret) because the whole point of the YC runtime mode is that one
// schedule definition works unchanged on both clouds.
const cleanupSchedulePayload = `{
  "requestId": "cleanup",
  "requestTime": "23/Jun/2024:15:48:12 +0000",
  "httpMethod": "POST",
  "path": "/api/cleanup",
  "requestContext": {
    "http": {
      "path": "/api/cleanup",
      "method": "POST",
      "protocol": "HTTP/1.1"
    }
  },
  "body": "{}",
  "headers": {
    "Authorization": "Bearer test-api-key"
  }
}`

func testService() *service {
	return &service{logger: logger.NewLogger()}
}

// timerEnvelope wraps payloads the way a Yandex timer trigger does.
func timerEnvelope(payloads ...string) string {
	messages := make([]map[string]any, 0, len(payloads))
	for i, payload := range payloads {
		messages = append(messages, map[string]any{
			"event_metadata": map[string]any{
				"event_id":   "b8f0a0b0-0000-0000-0000-00000000000" + string(rune('0'+i)),
				"event_type": yandexTimerEventType,
				"created_at": "2026-09-25T12:05:14.227761Z",
				"cloud_id":   "b1gvlrnlei4l5idm9cbj",
				"folder_id":  "b1g88tflru0ek1omtsu0",
			},
			"details": map[string]any{
				"trigger_id": "a1sfe084v4se7sb7uhk1",
				"payload":    payload,
			},
		})
	}
	body, err := json.Marshal(map[string]any{"messages": messages})
	if err != nil {
		panic(err)
	}
	return string(body)
}

// recordingHandler captures the request the router was ultimately given.
type recordingHandler struct {
	called  int
	request *http.Request
	body    string
	status  int
	reply   string
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.called++
	h.request = r
	if r.Body != nil {
		body, _ := io.ReadAll(r.Body)
		h.body = string(body)
	}
	if h.status != 0 {
		w.WriteHeader(h.status)
	}
	if h.reply != "" {
		_, _ = w.Write([]byte(h.reply))
	}
}

func TestIsYandexCloudRuntime(t *testing.T) {
	t.Setenv(cloudProviderEnv, "")
	assert.False(t, IsYandexCloudRuntime())

	t.Setenv(cloudProviderEnv, "aws")
	assert.False(t, IsYandexCloudRuntime())

	t.Setenv(cloudProviderEnv, cloudProviderYandex)
	assert.True(t, IsYandexCloudRuntime())

	// Tolerate case and stray whitespace — this value is written by a
	// provisioner into a YAML env map, not by a validator.
	t.Setenv(cloudProviderEnv, " Yandex ")
	assert.True(t, IsYandexCloudRuntime())
}

func TestYandexTriggerDispatchesScheduleToItsRoute(t *testing.T) {
	inner := &recordingHandler{status: http.StatusAccepted, reply: `{"deleted":3}`}
	handler := testService().yandexTriggerHandler(inner)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(timerEnvelope(cleanupSchedulePayload))))

	require.Equal(t, 1, inner.called)
	assert.Equal(t, http.MethodPost, inner.request.Method)
	assert.Equal(t, "/api/cleanup", inner.request.URL.Path)
	assert.Equal(t, "/api/cleanup", inner.request.RequestURI, "gin reads RequestURI, which a client-built request leaves empty")
	assert.Equal(t, "Bearer test-api-key", inner.request.Header.Get("Authorization"))
	assert.Equal(t, "application/json", inner.request.Header.Get("Content-Type"))
	assert.Equal(t, "{}", inner.body)

	// The service's own response reaches the trigger unaltered, so its retry
	// policy sees the real status.
	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, `{"deleted":3}`, rec.Body.String())
}

func TestYandexTriggerReadsApiGatewayShapedPayload(t *testing.T) {
	// No requestContext at all — the shape an api-gateway-routed lambda gets.
	inner := &recordingHandler{}
	handler := testService().yandexTriggerHandler(inner)

	payload := `{"httpMethod":"PUT","path":"/api/tick","body":"ping"}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(timerEnvelope(payload))))

	require.Equal(t, 1, inner.called)
	assert.Equal(t, http.MethodPut, inner.request.Method)
	assert.Equal(t, "/api/tick", inner.request.URL.Path)
	assert.Equal(t, "ping", inner.body)
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestYandexTriggerCarriesQueryAndBase64Body(t *testing.T) {
	inner := &recordingHandler{}
	handler := testService().yandexTriggerHandler(inner)

	payload, err := json.Marshal(map[string]any{
		"path":                  "/api/tick",
		"httpMethod":            "POST",
		"queryStringParameters": map[string]string{"force": "true"},
		"isBase64Encoded":       true,
		"body":                  base64.StdEncoding.EncodeToString([]byte(`{"n":1}`)),
	})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(timerEnvelope(string(payload)))))

	require.Equal(t, 1, inner.called)
	assert.Equal(t, "force=true", inner.request.URL.RawQuery)
	assert.Equal(t, `{"n":1}`, inner.body)
}

func TestYandexTriggerPassesThroughNonEnvelopeBody(t *testing.T) {
	inner := &recordingHandler{}
	handler := testService().yandexTriggerHandler(inner)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"hello":"world"}`)))

	require.Equal(t, 1, inner.called)
	assert.Equal(t, "/", inner.request.URL.Path)
	assert.Equal(t, `{"hello":"world"}`, inner.body, "a non-trigger body must reach the route untouched")
}

func TestYandexTriggerPassesThroughNonRootAndNonPost(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
	}{
		{"GET root", http.MethodGet, "/"},
		{"POST elsewhere", http.MethodPost, "/api/cleanup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := &recordingHandler{}
			h := testService().yandexTriggerHandler(inner)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader(timerEnvelope(cleanupSchedulePayload))))

			require.Equal(t, 1, inner.called)
			assert.Equal(t, tc.path, inner.request.URL.Path, "the envelope must only be unwrapped on POST /")
		})
	}
}

func TestYandexTriggerIgnoresNonTimerEventTypes(t *testing.T) {
	inner := &recordingHandler{}
	handler := testService().yandexTriggerHandler(inner)

	// An Object Storage trigger: same envelope, different details shape. Acting
	// on it as if it were a schedule would dispatch nonsense.
	body := `{"messages":[{"event_metadata":{"event_type":"yandex.cloud.events.storage.ObjectCreate"},"details":{"bucket_id":"b"}}]}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))

	require.Equal(t, 1, inner.called)
	assert.Equal(t, "/", inner.request.URL.Path)
	assert.Equal(t, body, inner.body)
}

func TestYandexTriggerRejectsUnusablePayload(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
	}{
		{"not json", "this is not json"},
		{"no path", `{"httpMethod":"POST","body":"{}"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := &recordingHandler{}
			handler := testService().yandexTriggerHandler(inner)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(timerEnvelope(tc.payload))))

			assert.Equal(t, 0, inner.called)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestYandexTriggerSummarisesBatchedMessages(t *testing.T) {
	inner := &recordingHandler{status: http.StatusInternalServerError}
	handler := testService().yandexTriggerHandler(inner)

	envelope := timerEnvelope(cleanupSchedulePayload, `{"path":"/api/tick","httpMethod":"POST"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(envelope)))

	require.Equal(t, 2, inner.called)
	// N responses cannot share one ResponseWriter, so a batch is summarised —
	// and a failure among them surfaces, or the trigger would never retry.
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var summary struct {
		Dispatched []struct {
			Path   string `json:"path"`
			Status int    `json:"status"`
		} `json:"dispatched"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &summary))
	require.Len(t, summary.Dispatched, 2)
	assert.Equal(t, "/api/cleanup", summary.Dispatched[0].Path)
	assert.Equal(t, "/api/tick", summary.Dispatched[1].Path)
}
