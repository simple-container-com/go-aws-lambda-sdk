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
	echoadapter "github.com/its-felix/aws-lambda-go-http-adapter/adapter"
	echohandler "github.com/its-felix/aws-lambda-go-http-adapter/handler"
	"github.com/labstack/echo/v4"
	"github.com/pkg/errors"
	"github.com/samber/lo"
	echoSwagger "github.com/swaggo/echo-swagger"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/awsutil"
	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

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
	GetMeta(ctx context.Context) ResultMeta
	GinAdapter() *ginadapter.GinLambda
}

type service struct {
	ctx                           context.Context
	apiKey                        string
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
	httpRouter                    HttpAdapterRouter
	lambdaStartFunc               any
	lambdaSize                    float64
	lambdaCostPerMbPerMillisecond float64
	useResponseStreaming          bool
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

	if os.Getenv("LOCAL_DEBUG") == "true" {
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
	invokeMode := os.Getenv("SIMPLE_CONTAINER_AWS_LAMBDA_INVOKE_MODE")
	if invokeMode != "" {
		if invokeMode == "RESPONSE_STREAM" {
			opts = append([]Option{UseResponseStreaming(true)}, opts...)
		}
	}

	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard

	s := &service{
		ctx: ctx,
	}

	s.logger = log
	for _, opt := range opts {
		opt(s)
	}

	var router http.Handler
	if s.httpRouter == nil && s.useResponseStreaming {
		log.Infof(ctx, "setting up echo router")
		echoRouter, err := s.initEchoAdapter()
		if err != nil {
			return nil, errors.Wrapf(err, "failed to init echo router")
		}
		router = echoRouter
		s.httpRouter = EchoRouter(echoRouter, s.logger, s.localDebugMode)
		s.lambdaStartFunc = echohandler.NewFunctionURLStreamingHandler(echoadapter.NewEchoAdapter(echoRouter))
		echoRouter.GET("/api/swagger/*", echoSwagger.WrapHandler)
	} else if s.httpRouter == nil {
		log.Infof(ctx, "setting up gin router")
		ginRouter := gin.New()
		s.httpRouter = GinRouter(ginRouter, s.logger, s.localDebugMode)
		ginRouter.Use(gin.Recovery())
		s.lambdaAdapter = ginadapter.New(ginRouter)
		router = ginRouter
		switch s.routingType {
		case lambdaRoutingTypeFunctionUrl:
			s.lambdaStartFunc = s.ProxyLambdaFunctionURL
		case lambdaRoutingTypeApiGw:
			s.lambdaStartFunc = s.ProxyLambdaApiGateway
		default:
			return nil, errors.Errorf("Unknown routing type: %q \n", s.routingType)
		}
		ginRouter.Use(func(c *gin.Context) {
			if c.Request.RequestURI == "/api/swagger" || c.Request.RequestURI == "/api/swagger/" {
				c.Request.RequestURI = "/api/swagger/index.html"
			}
		})
		ginRouter.GET("/api/swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))
	}

	s.server = &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%s", lo.If(s.port != "", s.port).Else("8080")),
		Handler: router,
	}

	s.skipAuthRoutes = append(s.skipAuthRoutes, "/api/status")

	if s.registerRoutesCallback == nil {
		return nil, errors.Errorf("register routes callback is not set")
	}
	s.httpRouter.Use(s.requestUIDMiddleware())
	s.httpRouter.Use(s.debugLogMiddleware())
	if s.apiKey != "" {
		s.httpRouter.Use(s.apiKeyAuthMiddleware())
	}
	if s.registerStatusEndpoint == nil || lo.FromPtr(s.registerStatusEndpoint) {
		s.httpRouter.GET("/api/status", s.statusEndpoint)
	}

	if err := s.registerRoutesCallback(s.httpRouter); err != nil {
		return nil, errors.Wrapf(err, "failed to register routes")
	}

	ctx, cancel := context.WithCancel(ctx)
	s.cancels = append(s.cancels, cancel)
	s.ctx = ctx

	return s, nil
}

func (s *service) initEchoAdapter() (*echo.Echo, error) {
	e := echo.New()
	return e, nil
}

func (s *service) GetMeta(ctx context.Context) ResultMeta {
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
		s.Logger().Infof(context.Background(), "starting lambda handler...")
		lambda.Start(s.lambdaStartFunc)
		s.Logger().Infof(context.Background(), "finished lambda handler...")
		return nil
	}
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
	if s.lambdaAdapter == nil {
		return events.APIGatewayProxyResponse{}, errors.Errorf("lambda adapter is not configure, are you using gin adapter?")
	}
	return s.lambdaAdapter.ProxyWithContext(ctx, request)
}

func (s *service) ProxyLambdaFunctionURL(ctx context.Context, request events.LambdaFunctionURLRequest) (any, error) {
	apiGwReq := awsutil.ToAPIGatewayRequest(request)
	if s.lambdaAdapter == nil {
		return events.APIGatewayProxyResponse{}, errors.Errorf("lambda adapter is not configure, are you using gin adapter?")
	}
	res, err := s.lambdaAdapter.ProxyWithContext(ctx, apiGwReq)
	if err != nil {
		return events.LambdaFunctionURLResponse{}, errors.Wrapf(err, "failed to process request")
	}
	return awsutil.ToLambdaFunctionURLResponse(res), nil
}
