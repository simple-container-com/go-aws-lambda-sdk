package service

import (
	"github.com/gin-gonic/gin"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

type (
	Option                 func(*service)
	RegisterRoutesCallback func(router *gin.Engine) error
)

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

func WithPort(port string) Option {
	return func(s *service) {
		s.port = port
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
