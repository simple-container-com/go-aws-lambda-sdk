package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/pkg/errors"
)

const (
	// cloudProviderEnv names the cloud Simple Container deployed this service to.
	//
	// It has to come from the provisioner because Yandex does not identify itself
	// at runtime: the Serverless Containers runtime defines exactly two variables,
	// PORT and REQUEST_PATH, and neither says which cloud you are on. Detecting YC
	// by the *absence* of AWS_LAMBDA_RUNTIME_API was rejected — it would silently
	// turn any misconfigured Lambda into an HTTP server that never serves a
	// request. api/pkg/clouds/pulumi/yandex/serverless_container.go sets this.
	cloudProviderEnv = "SIMPLE_CONTAINER_CLOUD"

	// cloudProviderYandex is the value of cloudProviderEnv on Yandex Cloud.
	cloudProviderYandex = "yandex"

	// yandexTimerEventType is the event_type a Yandex timer trigger stamps on
	// every message it delivers.
	yandexTimerEventType = "yandex.cloud.events.serverless.triggers.TimerMessage"

	// yandexTriggerPath is the path a trigger POSTs its envelope to. A Serverless
	// Container is a plain HTTP server, so a trigger invocation arrives as an
	// ordinary request to the container root, not on a separate event channel.
	yandexTriggerPath = "/"

	// yandexTriggerMaxBodyBytes caps how much of a root POST is read while
	// deciding whether it is a trigger envelope. YC caps a trigger payload at
	// 4 KB and the envelope adds a few hundred bytes; the slack is for headroom.
	// A larger body is still passed through untouched — see yandexTriggerHandler.
	yandexTriggerMaxBodyBytes = 64 << 10
)

// IsYandexCloudRuntime reports whether this process is running as a Yandex Cloud
// Serverless Container rather than an AWS Lambda.
//
// Services can use it to pick a cloud-specific code path of their own (an S3
// endpoint override, say). The SDK itself uses it to serve HTTP instead of
// starting the Lambda runtime loop.
func IsYandexCloudRuntime() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(cloudProviderEnv)), cloudProviderYandex)
}

type (
	// yandexTriggerEnvelope is what a Yandex trigger POSTs to the container.
	// Shape: cloud.yandex.com/docs → Serverless Containers → trigger → timer.
	yandexTriggerEnvelope struct {
		Messages []yandexTriggerMessage `json:"messages"`
	}

	yandexTriggerMessage struct {
		EventMetadata yandexEventMetadata  `json:"event_metadata"`
		Details       yandexTriggerDetails `json:"details"`
	}

	yandexEventMetadata struct {
		EventID   string `json:"event_id"`
		EventType string `json:"event_type"`
		CreatedAt string `json:"created_at"`
		CloudID   string `json:"cloud_id"`
		FolderID  string `json:"folder_id"`
	}

	yandexTriggerDetails struct {
		TriggerID string `json:"trigger_id"`
		// Payload is the operator-supplied string configured on the trigger. For
		// an SC-provisioned schedule it holds the same JSON the AWS EventBridge
		// target sends the Lambda, so one `schedules:` entry in client.yaml works
		// on both clouds.
		Payload string `json:"payload"`
	}

	// yandexScheduleRequest is the request a schedule's payload describes.
	//
	// It deliberately reads BOTH spellings. A Function URL lambda gets
	// requestContext.http.{method,path}; an API Gateway lambda gets the top-level
	// httpMethod/path; and the schedules in the fleet's client.yaml files carry
	// both, because the routing type is a deploy-time choice the schedule author
	// should not have to track.
	yandexScheduleRequest struct {
		HTTPMethod            string            `json:"httpMethod"`
		Path                  string            `json:"path"`
		RawPath               string            `json:"rawPath"`
		RawQueryString        string            `json:"rawQueryString"`
		QueryStringParameters map[string]string `json:"queryStringParameters"`
		Headers               map[string]string `json:"headers"`
		Body                  string            `json:"body"`
		IsBase64Encoded       bool              `json:"isBase64Encoded"`
		RequestContext        struct {
			HTTP struct {
				Method string `json:"method"`
				Path   string `json:"path"`
			} `json:"http"`
		} `json:"requestContext"`
	}
)

// yandexTriggerHandler turns a Yandex trigger invocation into a request against
// the service's own routes.
//
// A Lambda receives a schedule as an event and the SDK converts it into a
// request; a Serverless Container instead receives an HTTP POST to `/` whose
// body is the trigger envelope, with the schedule's request JSON as an opaque
// string inside it. Without this unwrapping a scheduled job on YC hits `/`,
// matches no route, 404s, and the trigger retries forever against nothing.
//
// Anything that is not a trigger envelope is passed through untouched, body
// included.
func (s *service) yandexTriggerHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != yandexTriggerPath {
			next.ServeHTTP(w, r)
			return
		}

		peek, err := io.ReadAll(io.LimitReader(r.Body, yandexTriggerMaxBodyBytes))
		if err != nil {
			s.logger.Warnf(r.Context(), "failed to read root POST body: %v", err)
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}
		// Restore what was consumed. MultiReader rather than a plain reader over
		// `peek`, so a body larger than the cap survives the pass-through intact.
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(peek), r.Body))

		messages := decodeYandexTriggerMessages(peek)
		if len(messages) == 0 {
			next.ServeHTTP(w, r)
			return
		}

		s.dispatchYandexTriggerMessages(w, r, next, messages)
	})
}

// decodeYandexTriggerMessages returns the trigger messages in body, or nil when
// body is not a trigger envelope.
func decodeYandexTriggerMessages(body []byte) []yandexTriggerMessage {
	if !json.Valid(body) {
		return nil
	}
	var envelope yandexTriggerEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil
	}
	return timerTriggerMessages(envelope.Messages)
}

// timerTriggerMessages keeps only the messages this SDK knows how to act on.
// Today that is timer messages; other trigger types (object storage, message
// queue) deliver a different `details` shape and are left to fall through to the
// service's own routes rather than being mis-dispatched.
func timerTriggerMessages(messages []yandexTriggerMessage) []yandexTriggerMessage {
	out := make([]yandexTriggerMessage, 0, len(messages))
	for _, m := range messages {
		if m.EventMetadata.EventType == yandexTimerEventType && strings.TrimSpace(m.Details.Payload) != "" {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// dispatchYandexTriggerMessages runs each message's request against the router.
//
// A timer delivers exactly one message per invocation, and that case writes
// straight through to w so the service's real status, headers and body reach the
// trigger unaltered. Batches are buffered instead, because N responses cannot
// share one ResponseWriter, and answered with a summary.
func (s *service) dispatchYandexTriggerMessages(
	w http.ResponseWriter, r *http.Request, next http.Handler, messages []yandexTriggerMessage,
) {
	if len(messages) == 1 {
		req, err := yandexScheduleHTTPRequest(r, messages[0].Details.Payload)
		if err != nil {
			s.logTriggerError(r, messages[0], err)
			http.Error(w, "invalid trigger payload", http.StatusBadRequest)
			return
		}
		s.logger.Infof(r.Context(), "dispatching yandex trigger %q to %s %s",
			messages[0].Details.TriggerID, req.Method, req.URL.Path)
		next.ServeHTTP(w, req)
		return
	}

	type dispatched struct {
		TriggerID string `json:"triggerId"`
		Method    string `json:"method,omitempty"`
		Path      string `json:"path,omitempty"`
		Status    int    `json:"status"`
		Error     string `json:"error,omitempty"`
	}
	results := make([]dispatched, 0, len(messages))
	status := http.StatusOK
	for _, msg := range messages {
		req, err := yandexScheduleHTTPRequest(r, msg.Details.Payload)
		if err != nil {
			s.logTriggerError(r, msg, err)
			results = append(results, dispatched{
				TriggerID: msg.Details.TriggerID, Status: http.StatusBadRequest, Error: err.Error(),
			})
			status = http.StatusBadRequest
			continue
		}
		s.logger.Infof(r.Context(), "dispatching yandex trigger %q to %s %s",
			msg.Details.TriggerID, req.Method, req.URL.Path)
		buf := newBufferedResponse()
		next.ServeHTTP(buf, req)
		results = append(results, dispatched{
			TriggerID: msg.Details.TriggerID, Method: req.Method, Path: req.URL.Path, Status: buf.status,
		})
		if buf.status >= http.StatusBadRequest && status == http.StatusOK {
			// Surface a failure so the trigger's retry policy sees it.
			status = buf.status
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{"dispatched": results}); err != nil {
		s.logger.Warnf(r.Context(), "failed to write trigger dispatch summary: %v", err)
	}
}

func (s *service) logTriggerError(r *http.Request, msg yandexTriggerMessage, err error) {
	// The payload itself is not logged: a schedule's request carries an
	// Authorization header, which is how the fleet's scheduled jobs authenticate.
	s.logger.Errorf(r.Context(), "failed to build request from yandex trigger %q: %v", msg.Details.TriggerID, err)
}

// yandexScheduleHTTPRequest builds the in-process request a schedule describes.
// It is dispatched straight into the router, never over the network.
func yandexScheduleHTTPRequest(orig *http.Request, payload string) (*http.Request, error) {
	var sched yandexScheduleRequest
	if err := json.Unmarshal([]byte(strings.TrimSpace(payload)), &sched); err != nil {
		return nil, errors.Wrapf(err, "failed to parse trigger payload as a scheduled request")
	}

	method := firstNonEmpty(sched.RequestContext.HTTP.Method, sched.HTTPMethod, http.MethodPost)
	path := firstNonEmpty(sched.RequestContext.HTTP.Path, sched.Path, sched.RawPath)
	if path == "" {
		return nil, errors.Errorf("trigger payload specifies no path to dispatch to")
	}

	body := sched.Body
	if sched.IsBase64Encoded {
		decoded, err := base64.StdEncoding.DecodeString(body)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to base64-decode trigger payload body")
		}
		body = string(decoded)
	}

	target := path
	if query := scheduleQuery(sched); query != "" {
		target = path + "?" + query
	}

	req, err := http.NewRequestWithContext(orig.Context(), method, target, strings.NewReader(body))
	if err != nil {
		return nil, errors.Wrapf(err, "failed to build request for %s %s", method, target)
	}
	for name, value := range sched.Headers {
		req.Header.Set(name, value)
	}
	if body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	// Carry over what identifies the caller to a server-side handler. RequestURI
	// in particular is empty on a client-built request, and gin reads it.
	req.Host = orig.Host
	req.RemoteAddr = orig.RemoteAddr
	req.RequestURI = target
	return req, nil
}

func scheduleQuery(sched yandexScheduleRequest) string {
	if sched.RawQueryString != "" {
		return sched.RawQueryString
	}
	if len(sched.QueryStringParameters) == 0 {
		return ""
	}
	values := url.Values{}
	for name, value := range sched.QueryStringParameters {
		values.Set(name, value)
	}
	return values.Encode()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// bufferedResponse collects a handler's response so several can be run against
// one inbound request.
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: http.Header{}, status: http.StatusOK}
}

func (b *bufferedResponse) Header() http.Header { return b.header }

func (b *bufferedResponse) WriteHeader(status int) { b.status = status }

func (b *bufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }
