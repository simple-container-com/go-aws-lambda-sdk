package service

import (
	"context"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/labstack/echo/v4"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

type HttpWriterFlusher interface {
	http.ResponseWriter
	http.Flusher
}

type HttpAdapterRouter interface {
	Use(mw HttpAdapterHandler)
	Any(p string, h HttpAdapterHandler)
	GET(p string, h HttpAdapterHandler)
	POST(p string, h HttpAdapterHandler)
	DELETE(p string, h HttpAdapterHandler)
	PATCH(p string, h HttpAdapterHandler)
	PUT(p string, h HttpAdapterHandler)
	OPTIONS(p string, h HttpAdapterHandler)
	HEAD(p string, h HttpAdapterHandler)
	Group(name string) HttpAdapterRouter
}

type HttpAdapterHandler func(h HttpAdapter) error

type HttpAdapter interface {
	Context() context.Context
	SetContext(ctx context.Context)
	SetHeader(name, value string)
	Writer() HttpWriterFlusher
	JSON(code int, obj any)
	RequestBody() io.Reader
	Request() *http.Request
	AbortWithStatus(status int)
	RemoteIP() string
	Query(name string) string
	FormFile(name string) (*multipart.FileHeader, error)
	MultipartForm() (*multipart.Form, error)
}

type ginAdapter struct {
	c          *gin.Context
	localDebug bool
	logger     logger.Logger
}

func (g *ginAdapter) Query(name string) string {
	return g.c.Query(name)
}

func (g *ginAdapter) FormFile(name string) (*multipart.FileHeader, error) {
	return g.c.FormFile(name)
}

func (g *ginAdapter) MultipartForm() (*multipart.Form, error) {
	return g.c.MultipartForm()
}

func (g *ginAdapter) SetContext(ctx context.Context) {
	g.c.Request = g.Request().WithContext(ctx)
}

func (g *ginAdapter) AbortWithStatus(status int) {
	g.c.AbortWithStatus(status)
}

func (g *ginAdapter) RemoteIP() string {
	return g.c.RemoteIP()
}

type echoAdapter struct {
	c          echo.Context
	localDebug bool
	logger     logger.Logger
}

func (e *echoAdapter) Query(name string) string {
	return e.c.QueryParam(name)
}

func (e *echoAdapter) FormFile(name string) (*multipart.FileHeader, error) {
	return e.c.FormFile(name)
}

func (e *echoAdapter) MultipartForm() (*multipart.Form, error) {
	return e.c.MultipartForm()
}

func (e *echoAdapter) SetContext(ctx context.Context) {
	e.c.SetRequest(e.c.Request().WithContext(ctx))
}

func (e *echoAdapter) AbortWithStatus(status int) {
	e.c.Response().WriteHeader(status)
}

func (e *echoAdapter) RemoteIP() string {
	ip, _, err := net.SplitHostPort(strings.TrimSpace(e.Request().RemoteAddr))
	if err != nil {
		return ""
	}
	return ip
}

func (e *echoAdapter) Context() context.Context {
	return e.c.Request().Context()
}

func (e *echoAdapter) SetHeader(name, value string) {
	e.c.Response().Header().Set(name, value)
}

type withEchoFlusher struct {
	http.ResponseWriter
	c          echo.Context
	localDebug bool
}

func (w *withEchoFlusher) Flush() {
	if w.localDebug {
		w.c.Response().Flush()
	}
}

func (e *echoAdapter) Writer() HttpWriterFlusher {
	return &withEchoFlusher{
		ResponseWriter: e.c.Response().Writer,
		c:              e.c,
		localDebug:     e.localDebug,
	}
}

func (e *echoAdapter) JSON(code int, obj any) {
	_ = e.c.JSON(code, obj)
}

func (e *echoAdapter) Request() *http.Request {
	return e.c.Request()
}

func (e *echoAdapter) RequestBody() io.Reader {
	return e.c.Request().Body
}

func EchoAdapter(callback func(c HttpAdapter) error, logger logger.Logger, localDebug bool) func(c echo.Context) error {
	return func(c echo.Context) error {
		return callback(&echoAdapter{
			c:          c,
			localDebug: localDebug,
			logger:     logger,
		})
	}
}

func GinAdapter(callback func(c HttpAdapter) error, logger logger.Logger, localDebug bool) func(*gin.Context) {
	return func(g *gin.Context) {
		if err := callback(&ginAdapter{
			c:          g,
			localDebug: localDebug,
			logger:     logger,
		}); err != nil {
			logger.Errorf(logger.WithValue(g.Request.Context(), "error", err.Error()), "failed to process request")
			g.AbortWithStatus(500)
		}
	}
}

func GinRouter(engine gin.IRouter, logger logger.Logger, debugMode bool) HttpAdapterRouter {
	return &ginRouter{
		router:     engine,
		localDebug: debugMode,
		logger:     logger,
	}
}

func EchoRouter(engine *echo.Echo, logger logger.Logger, debugMode bool) HttpAdapterRouter {
	return &echoRouter{
		router:     engine,
		logger:     logger,
		localDebug: debugMode,
	}
}

func (g *ginRouter) Use(mw HttpAdapterHandler) {
	g.router.Use(func(c *gin.Context) {
		adapter := g.newGinAdapter(c)
		if err := mw(adapter); err != nil {
			c.AbortWithStatus(500)
			g.logger.Errorf(g.logger.WithValue(c.Request.Context(), "error", err.Error()), "error while processing middleware")
			return
		}
		c.Next()
	})
}

type ginRouter struct {
	router     gin.IRouter
	localDebug bool
	logger     logger.Logger
}

func (g *ginRouter) Group(name string) HttpAdapterRouter {
	return GinRouter(g.router.Group(name), g.logger, g.localDebug)
}

func (g *ginRouter) Any(p string, h HttpAdapterHandler) {
	g.router.Any(p, GinAdapter(h, g.logger, g.localDebug))
}

func (g *ginRouter) GET(p string, h HttpAdapterHandler) {
	g.router.GET(p, GinAdapter(h, g.logger, g.localDebug))
}

func (g *ginRouter) POST(p string, h HttpAdapterHandler) {
	g.router.POST(p, GinAdapter(h, g.logger, g.localDebug))
}

func (g *ginRouter) DELETE(p string, h HttpAdapterHandler) {
	g.router.DELETE(p, GinAdapter(h, g.logger, g.localDebug))
}

func (g *ginRouter) PATCH(p string, h HttpAdapterHandler) {
	g.router.PATCH(p, GinAdapter(h, g.logger, g.localDebug))
}

func (g *ginRouter) PUT(p string, h HttpAdapterHandler) {
	g.router.PUT(p, GinAdapter(h, g.logger, g.localDebug))
}

func (g *ginRouter) OPTIONS(p string, h HttpAdapterHandler) {
	g.router.OPTIONS(p, GinAdapter(h, g.logger, g.localDebug))
}

func (g *ginRouter) HEAD(p string, h HttpAdapterHandler) {
	g.router.HEAD(p, GinAdapter(h, g.logger, g.localDebug))
}

func (g *ginRouter) newGinAdapter(c *gin.Context) HttpAdapter {
	return &ginAdapter{
		c:          c,
		localDebug: g.localDebug,
	}
}

type echoRouter struct {
	router     *echo.Echo
	localDebug bool
	logger     logger.Logger
}

type echoGroup struct {
	router     *echo.Group
	localDebug bool
	logger     logger.Logger
}

func (e *echoGroup) Group(name string) HttpAdapterRouter {
	return &echoGroup{
		router:     e.router.Group(name),
		localDebug: e.localDebug,
		logger:     e.logger,
	}
}

func (e *echoRouter) Group(prefix string) HttpAdapterRouter {
	return &echoGroup{
		router:     e.router.Group(prefix),
		localDebug: e.localDebug,
		logger:     e.logger,
	}
}

func (e *echoGroup) Any(p string, h HttpAdapterHandler) {
	e.router.Any(p, EchoAdapter(h, e.logger, e.localDebug))
}

func (e *echoGroup) GET(p string, h HttpAdapterHandler) {
	e.router.GET(p, EchoAdapter(h, e.logger, e.localDebug))
}

func (e *echoGroup) POST(p string, h HttpAdapterHandler) {
	e.router.POST(p, EchoAdapter(h, e.logger, e.localDebug))
}

func (e *echoGroup) DELETE(p string, h HttpAdapterHandler) {
	e.router.DELETE(p, EchoAdapter(h, e.logger, e.localDebug))
}

func (e *echoGroup) PATCH(p string, h HttpAdapterHandler) {
	e.router.PATCH(p, EchoAdapter(h, e.logger, e.localDebug))
}

func (e *echoGroup) PUT(p string, h HttpAdapterHandler) {
	e.router.PUT(p, EchoAdapter(h, e.logger, e.localDebug))
}

func (e *echoGroup) OPTIONS(p string, h HttpAdapterHandler) {
	e.router.OPTIONS(p, EchoAdapter(h, e.logger, e.localDebug))
}

func (e *echoGroup) HEAD(p string, h HttpAdapterHandler) {
	e.router.HEAD(p, EchoAdapter(h, e.logger, e.localDebug))
}

func (e *echoGroup) Use(mw HttpAdapterHandler) {
	e.router.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if err := EchoAdapter(mw, e.logger, e.localDebug)(c); err != nil {
				return err
			}
			return next(c)
		}
	})
}

func (e *echoRouter) Any(p string, h HttpAdapterHandler) {
	e.router.Any(p, EchoAdapter(h, e.logger, e.localDebug))
}

func (e *echoRouter) GET(p string, h HttpAdapterHandler) {
	e.router.GET(p, EchoAdapter(h, e.logger, e.localDebug))
}

func (e *echoRouter) POST(p string, h HttpAdapterHandler) {
	e.router.POST(p, EchoAdapter(h, e.logger, e.localDebug))
}

func (e *echoRouter) DELETE(p string, h HttpAdapterHandler) {
	e.router.DELETE(p, EchoAdapter(h, e.logger, e.localDebug))
}

func (e *echoRouter) PATCH(p string, h HttpAdapterHandler) {
	e.router.PATCH(p, EchoAdapter(h, e.logger, e.localDebug))
}

func (e *echoRouter) PUT(p string, h HttpAdapterHandler) {
	e.router.PUT(p, EchoAdapter(h, e.logger, e.localDebug))
}

func (e *echoRouter) OPTIONS(p string, h HttpAdapterHandler) {
	e.router.OPTIONS(p, EchoAdapter(h, e.logger, e.localDebug))
}

func (e *echoRouter) HEAD(p string, h HttpAdapterHandler) {
	e.router.HEAD(p, EchoAdapter(h, e.logger, e.localDebug))
}

func (e *echoRouter) Use(mw HttpAdapterHandler) {
	e.router.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if err := EchoAdapter(mw, e.logger, e.localDebug)(c); err != nil {
				return err
			}
			return next(c)
		}
	})
}

func (g *ginAdapter) Request() *http.Request {
	return g.c.Request
}

func (g *ginAdapter) Context() context.Context {
	return g.c.Request.Context()
}

func (g *ginAdapter) SetHeader(name, value string) {
	g.c.Writer.Header().Set(name, value)
}

func (g *ginAdapter) Next(h HttpAdapter) {
	g.c.Next()
}

func (g *ginAdapter) Writer() HttpWriterFlusher {
	return g.c.Writer
}

func (g *ginAdapter) JSON(code int, obj any) {
	g.c.JSON(code, obj)
}

func (g *ginAdapter) RequestBody() io.Reader {
	return g.c.Request.Body
}
