package service

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/samber/lo"
)

const (
	RequestUIDKey     = "requestUID"
	RequestStartedKey = "requestStartedAt"
)

type ResultMeta struct {
	Error             *string       `json:"error,omitempty" yaml:"error,omitempty"` // whether error happened whilst processing
	RequestUID        string        `json:"requestUID" yaml:"requestUID"`           // unique identifier of job for debugging purposes
	RequestStartedAt  time.Time     `json:"requestStartedAt" yaml:"requestStartedAt"`
	RequestFinishedAt time.Time     `json:"requestFinishedAt" yaml:"requestFinishedAt"`
	RequestTime       time.Duration `json:"requestTime" yaml:"requestTime"`
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

func (s *service) reportStatus(c *gin.Context,
	status *Status,
) {
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
func (s *service) statusEndpoint(c *gin.Context) {
	s.reportStatus(c, s.Status())
}

func (s *service) Status() *Status {
	res := Status{
		Status: "running",
	}
	return &res
}

func (s *service) requestUIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()

		requestUID, err := uuid.NewUUID()
		if err != nil {
			return
		}
		ctx = s.logger.WithValue(ctx, RequestUIDKey, requestUID.String())
		ctx = s.logger.WithValue(ctx, RequestStartedKey, time.Now())
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func (s *service) debugLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.requestDebugMode {
			requestUIDOrNil := s.logger.GetValue(c.Request.Context(), RequestUIDKey)
			requestUID := "<nil>"
			if requestUIDOrNil != nil {
				requestUID = requestUIDOrNil.(string)
			}
			ctx := c.Request.Context()
			ctx = s.logger.WithValue(ctx, "request", map[string]any{
				"method":     c.Request.Method,
				"requestURI": c.Request.RequestURI,
				"headers":    c.Request.Header,
				"host":       c.Request.Host,
				"proto":      c.Request.Proto,
				"remoteIP":   c.RemoteIP(),
				"requestUID": requestUID,
			})
			s.logger.Infof(ctx, "got request")
			c.Header("X-Request-UID", requestUID)
		}
		c.Next()
	}
}

func (s *service) apiKeyAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.apiKey == "" {
			s.logger.Errorf(s.ctx, "API_KEY is not configured")
			s.respondUnauthorized(c)
			return
		}

		if _, found := lo.Find(s.skipAuthRoutes, func(prefix string) bool {
			return strings.HasPrefix(c.Request.RequestURI, prefix)
		}); found {
			s.logger.Infof(s.ctx, "skip authorization for "+c.Request.RequestURI+" ... ")
			c.Next()
			return
		}

		authHeader := c.Request.Header["Authorization"]
		if len(authHeader) == 0 {
			s.respondUnauthorized(c)
			return
		} else if providedTokenParts := strings.Split(authHeader[0], " "); len(providedTokenParts) < 2 {
			s.respondUnauthorized(c)
			return
		} else if providedTokenParts[1] != s.apiKey {
			s.respondUnauthorized(c)
			return
		}
		c.Next()
	}
}

func (s *service) respondUnauthorized(c *gin.Context) {
	c.JSON(http.StatusUnauthorized, gin.H{"message": "authorization key is not provided"})
	c.AbortWithStatus(http.StatusUnauthorized)
}
