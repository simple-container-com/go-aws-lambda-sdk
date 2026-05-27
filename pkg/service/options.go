package service

import (
	"net/http"

	"github.com/samber/lo"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

type (
	Option                 func(*service)
	RegisterRoutesCallback func(router HttpAdapterRouter) error
)

func UseResponseStreaming(use bool) Option {
	return func(s *service) {
		s.useResponseStreaming = use
	}
}

func WithHttpAdapterRouter(a HttpAdapterRouter) Option {
	return func(s *service) {
		s.httpRouter = a
	}
}

func WithLogger(logger logger.Logger) Option {
	return func(s *service) {
		s.logger = logger
	}
}

func WithRoutes(callback RegisterRoutesCallback) Option {
	return func(s *service) {
		s.registerRoutesCallback = callback
	}
}

func WithSkipAuthRoutes(routes ...string) Option {
	return func(s *service) {
		s.skipAuthRoutes = routes
	}
}

func WithApiKey(key string) Option {
	return func(s *service) {
		s.apiKey = key
	}
}

func WithRoutingType(routingType string) Option {
	return func(s *service) {
		s.routingType = routingType
	}
}

func WithPort(port string) Option {
	return func(s *service) {
		s.port = port
	}
}

func WithoutStatusEndpoint() Option {
	return func(s *service) {
		s.registerStatusEndpoint = lo.ToPtr(false)
	}
}

func WithLambdaCostPerMbPerMs(cost float64) Option {
	return func(s *service) {
		s.lambdaCostPerMbPerMillisecond = cost
	}
}

func WithLambdaSize(size float64) Option {
	return func(s *service) {
		s.lambdaSize = size
	}
}

func WithRequestDebugMode() Option {
	return func(s *service) {
		s.requestDebugMode = true
	}
}

func WithVersion(version string) Option {
	return func(s *service) {
		s.version = version
	}
}

func WithLocalDebugMode() Option {
	return func(s *service) {
		s.localDebugMode = true
	}
}

// WithVanillaHandler routes a plain http.Handler through the SDK's Lambda
// Function URL start path (the same its-felix handler the echo router uses).
// The handler owns all routing, middleware, and auth; the SDK skips its
// echo/gin router, built-in middleware, /api/status, and the RegisterRoutes
// callback. Pair with UseResponseStreaming(true) for RESPONSE_STREAM Function
// URLs.
func WithVanillaHandler(h http.Handler) Option {
	return func(s *service) {
		s.vanillaHandler = h
	}
}
