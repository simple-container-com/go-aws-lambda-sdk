package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/samber/lo"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/awsutil"
	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

var ginProxy *service

const (
	serviceVersionEnv            = "SIMPLE_CONTAINER_VERSION"
	lambdaRoutingTypeEnv         = "SIMPLE_CONTAINER_AWS_LAMBDA_ROUTING_TYPE"
	lambdaRoutingTypeFunctionUrl = "function-url"
	lambdaRoutingTypeApiGw       = "api-gateway"
)

type Service interface {
	Start() error
	Logger() logger.Logger
	IsLocalDebugMode() bool
	IsRequestDebugEnabled() bool
	Port() string
	Version() string
	GetMeta(c *gin.Context) ResultMeta
}

type service struct {
	ctx                    context.Context
	apiKey                 string
	router                 *gin.Engine
	cancels                []func()
	lambdaAdapter          *ginadapter.GinLambda
	server                 *http.Server
	localDebugMode         bool
	requestDebugMode       bool
	logger                 logger.Logger
	port                   string
	registerRoutesCallback RegisterRoutesCallback
	skipAuthRoutes         []string
	version                string
	routingType            string
	registerStatusEndpoint *bool
}

func New(ctx context.Context, opts ...Option) (Service, error) {
	log := logger.NewLogger()

	// stdout and stderr are sent to AWS CloudWatch Logs
	log.Infof(ctx, "Server cold start")

	if apiKey, err := awsutil.GetEnvOrSecret("API_KEY"); err != nil {
		log.Warnf(ctx, "Failed to get API_KEY secret: %v", err)
	} else {
		opts = append([]Option{WithApiKey(apiKey)}, opts...)
	}

	opts = append([]Option{WithVersion(os.Getenv(serviceVersionEnv))}, opts...)
	opts = append([]Option{WithRoutingType(os.Getenv(lambdaRoutingTypeEnv))}, opts...)

	if os.Getenv("REQUEST_DEBUG") != "" {
		opts = append([]Option{WithRequestDebugMode()}, opts...)
	}

	if os.Getenv("LOCAL_DEBUG") != "" {
		opts = append([]Option{WithLocalDebugMode()}, opts...)
	}
	if os.Getenv("PORT") != "" {
		opts = append([]Option{WithPort(os.Getenv("PORT"))}, opts...)
	}

	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	s := &service{
		ctx: ctx,
	}

	for _, opt := range opts {
		opt(s)
	}

	s.skipAuthRoutes = append(s.skipAuthRoutes, "/api/status")

	router := gin.New()
	if s.registerRoutesCallback == nil {
		return nil, errors.Errorf("register routes callback is not set")
	}
	router.Use(gin.Recovery())
	router.Use(s.requestUIDMiddleware())
	router.Use(s.debugLogMiddleware())
	if s.apiKey != "" {
		router.Use(s.apiKeyAuthMiddleware())
	}
	if s.registerStatusEndpoint == nil || lo.FromPtr(s.registerStatusEndpoint) {
		router.GET("/api/status", s.statusEndpoint)
	}

	if err := s.registerRoutesCallback(router); err != nil {
		return nil, errors.Wrapf(err, "failed to register routes")
	}

	s.router = router

	s.lambdaAdapter = ginadapter.New(router)
	s.server = &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%s", lo.If(s.port != "", s.port).Else("8080")),
		Handler: s.router,
	}

	ctx, cancel := context.WithCancel(ctx)
	s.cancels = append(s.cancels, cancel)
	s.ctx = ctx

	s.logger = log

	ginProxy = s
	return s, nil
}

func (s *service) GetMeta(c *gin.Context) ResultMeta {
	ctx := c.Request.Context()
	requestStartedAt := s.logger.GetValue(ctx, RequestStartedKey).(time.Time)
	return ResultMeta{
		RequestUID:        s.logger.GetValue(ctx, RequestUIDKey).(string),
		RequestStartedAt:  requestStartedAt,
		RequestTime:       time.Since(requestStartedAt),
		RequestFinishedAt: time.Now(),
	}
}

func (s *service) Start() error {
	if s.localDebugMode {
		return s.server.ListenAndServe()
	} else {
		switch s.routingType {
		case lambdaRoutingTypeFunctionUrl:
			lambda.Start(ginProxy.ProxyLambdaFunctionURL)
		case lambdaRoutingTypeApiGw:
			lambda.Start(ginProxy.ProxyLambdaApiGateway)
		default:
			return errors.Errorf("Unknown routing type: %q \n", s.routingType)
		}
	}
	return nil
}

func (s *service) Logger() logger.Logger {
	return s.logger
}

func (s *service) IsLocalDebugMode() bool {
	return s.localDebugMode
}

func (s *service) IsRequestDebugEnabled() bool {
	return s.requestDebugMode
}

func (s *service) Port() string {
	return s.port
}

func (s *service) Version() string {
	return s.version
}

func (s *service) ProxyLambdaApiGateway(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	return s.lambdaAdapter.ProxyWithContext(ctx, request)
}

func (s *service) ProxyLambdaFunctionURL(ctx context.Context, request events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	res, err := s.lambdaAdapter.ProxyWithContext(ctx, awsutil.ToAPIGatewayRequest(request))
	if err != nil {
		return events.LambdaFunctionURLResponse{}, errors.Wrapf(err, "failed to process request")
	}
	return awsutil.ToLambdaFunctionURLResponse(res), nil
}
