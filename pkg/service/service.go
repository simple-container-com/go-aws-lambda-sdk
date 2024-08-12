package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
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
	lambdaSizeMbEnv              = "SIMPLE_CONTAINER_AWS_LAMBDA_SIZE_MB"
	lambdaRoutingTypeFunctionUrl = "function-url"
	lambdaRoutingTypeApiGw       = "api-gateway"
	lambdaCostPerMbMs            = 1.62760742e-11
)

type Service interface {
	Start() error
	Logger() logger.Logger
	IsLocalDebugMode() bool
	IsRequestDebugEnabled() bool
	Port() string
	Version() string
	GetMeta(c *gin.Context) ResultMeta
	GinAdapter() *ginadapter.GinLambda
}

type StreamingResponseProcessor func(response events.APIGatewayProxyRequest) (events.LambdaFunctionURLStreamingResponse, error)

type service struct {
	ctx                           context.Context
	apiKey                        string
	router                        *gin.Engine
	cancels                       []func()
	lambdaAdapter                 *ginadapter.GinLambda
	server                        *http.Server
	localDebugMode                bool
	requestDebugMode              bool
	logger                        logger.Logger
	port                          string
	registerRoutesCallback        RegisterRoutesCallback
	skipAuthRoutes                []string
	version                       string
	routingType                   string
	registerStatusEndpoint        *bool
	lambdaSize                    float64
	lambdaCostPerMbPerMillisecond float64
	streamingResponseProcessors   map[string]StreamingResponseProcessor
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
	if os.Getenv(lambdaSizeMbEnv) != "" {
		sizeFloat, err := strconv.ParseFloat(os.Getenv(lambdaSizeMbEnv), 64)
		if err == nil {
			opts = append([]Option{WithLambdaSize(sizeFloat)}, opts...)
		} else {
			// default lambda size
			opts = append([]Option{WithLambdaSize(128)}, opts...)
		}
	}
	opts = append([]Option{WithLambdaCostPerMbPerMs(lambdaCostPerMbMs)}, opts...)

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
	requestFinishedAt := time.Now()
	requestTime := time.Since(requestStartedAt)
	cost := s.lambdaSize * float64(requestTime.Milliseconds()) * s.lambdaCostPerMbPerMillisecond
	return ResultMeta{
		RequestUID:        s.logger.GetValue(ctx, RequestUIDKey).(string),
		RequestStartedAt:  requestStartedAt,
		RequestTime:       requestTime,
		RequestFinishedAt: requestFinishedAt,
		Cost:              cost,
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

func (s *service) GinAdapter() *ginadapter.GinLambda {
	return s.lambdaAdapter
}

func (s *service) ProxyLambdaApiGateway(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	return s.lambdaAdapter.ProxyWithContext(ctx, request)
}

func (s *service) ProxyLambdaFunctionURL(ctx context.Context, request events.LambdaFunctionURLRequest) (any, error) {
	apiGwReq := awsutil.ToAPIGatewayRequest(request)
	if proc, ok := s.streamingResponseProcessors[apiGwReq.Path]; ok {
		return proc(apiGwReq)
	}
	res, err := s.lambdaAdapter.ProxyWithContext(ctx, apiGwReq)
	if err != nil {
		return events.LambdaFunctionURLResponse{}, errors.Wrapf(err, "failed to process request")
	}
	return awsutil.ToLambdaFunctionURLResponse(res), nil
}
