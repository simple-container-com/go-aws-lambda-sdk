package service

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/pkg/errors"
	"github.com/samber/lo"
)

const (
	RequestUIDKey     = "requestUID"
	RequestStartedKey = "requestStartedAt"
	IsAuthorizedKey   = "isAuthorized"
)

type ResultMeta struct {
	Error             *string       `json:"error,omitempty" yaml:"error,omitempty"` // whether error happened whilst processing
	RequestUID        string        `json:"requestUID" yaml:"requestUID"`           // unique identifier of job for debugging purposes
	RequestStartedAt  time.Time     `json:"requestStartedAt" yaml:"requestStartedAt"`
	RequestFinishedAt time.Time     `json:"requestFinishedAt" yaml:"requestFinishedAt"`
	RequestTime       time.Duration `json:"requestTime" yaml:"requestTime"`
	IsAuthorized      bool          `json:"isAuthorized" yaml:"isAuthorized"`
	Cost              float64       `json:"cost" yaml:"cost"`
}

type Error struct {
	Message string     `yaml:"message" json:"message"`
	Meta    ResultMeta `json:"meta" yaml:"meta"` // metadata related to processing
}

type Status struct {
	Status string   `json:"status" yaml:"status"`
	Errors []string `json:"errors,omitempty" yaml:"errors,omitempty"`
}

func ReadBytes(stream io.Reader) []byte {
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(stream)
	return buf.Bytes()
}

func (s *service) reportStatus(c HttpAdapter, status *Status) {
	c.JSON(http.StatusOK, gin.H{
		"version": s.version,
		"status":  status,
	})
}

// @Schemes
// @Description return service status
// @Tags status
// @Produce json
// @Success 200 {object} Status
// @Router /api/status [get]
func (s *service) statusEndpoint(c HttpAdapter) error {
	s.reportStatus(c, s.Status())
	return nil
}

func (s *service) Status() *Status {
	res := Status{
		Status: "running",
	}
	return &res
}

func (s *service) requestUIDMiddleware() HttpAdapterHandler {
	return func(c HttpAdapter) error {
		ctx := c.Context()

		requestUID, err := uuid.NewUUID()
		if err != nil {
			return err
		}
		ctx = s.logger.WithValue(ctx, RequestUIDKey, requestUID.String())
		ctx = s.logger.WithValue(ctx, RequestStartedKey, time.Now())

		c.SetContext(ctx)
		return nil
	}
}

func (s *service) debugLogMiddleware() HttpAdapterHandler {
	return func(c HttpAdapter) error {
		if s.requestDebugMode {
			requestUIDOrNil := s.logger.GetValue(c.Context(), RequestUIDKey)
			requestUID := "<nil>"
			if requestUIDOrNil != nil {
				requestUID = requestUIDOrNil.(string)
			}
			ctx := c.Context()
			ctx = s.logger.WithValue(ctx, "request", map[string]any{
				"method":     c.Request().Method,
				"requestURI": c.Request().RequestURI,
				"headers":    c.Request().Header,
				"host":       c.Request().Host,
				"proto":      c.Request().Proto,
				"remoteIP":   c.RemoteIP(),
				"requestUID": requestUID,
			})
			s.logger.Infof(ctx, "got request")
			c.SetHeader("X-Request-UID", requestUID)
		}
		return nil
	}
}

func (s *service) apiKeyAuthMiddleware() HttpAdapterHandler {
	return func(c HttpAdapter) error {
		err := s.checkAuthorized(c, true)
		if err != nil {
			s.respondUnauthorized(c)
		}
		return err
	}
}

func (s *service) respondUnauthorized(c HttpAdapter) {
	c.JSON(http.StatusUnauthorized, gin.H{"message": "authorization key is not provided"})
	c.AbortWithStatus(http.StatusUnauthorized)
}

func (s *service) tryApiKeyAuthMiddleware() HttpAdapterHandler {
	return func(c HttpAdapter) error {
		err := s.checkAuthorized(c, false)
		isAuthorized := err == nil
		ctx := c.Context()
		ctx = s.logger.WithValue(ctx, IsAuthorizedKey, isAuthorized)
		c.SetContext(ctx)
		return nil
	}
}

func (s *service) checkAuthorized(c HttpAdapter, skipAuth bool) error {
	if s.apiKey == "" {
		s.logger.Errorf(s.ctx, "API_KEY is not configured")
		return errors.Errorf("API_KEY is not configured")
	}

	if skipAuth {
		if _, found := lo.Find(s.skipAuthRoutes, func(prefix string) bool {
			return strings.HasPrefix(c.Request().RequestURI, prefix)
		}); found {
			s.logger.Infof(s.ctx, "skip authorization for "+c.Request().RequestURI+" ... ")
			return nil
		}
	}

	authHeader := c.Request().Header["Authorization"]
	if len(authHeader) == 0 {
		return errors.Errorf("Unauthorized")
	} else if providedTokenParts := strings.Split(authHeader[0], " "); len(providedTokenParts) < 2 {
		return errors.Errorf("Unauthorized")
	} else if providedTokenParts[1] != s.apiKey {
		return errors.Errorf("Unauthorized")
	}
	return nil
}
