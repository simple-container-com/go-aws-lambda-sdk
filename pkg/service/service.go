package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/samber/lo"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/awsutil"
	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

var ginProxy Service

const (
	lambdaRoutingTypeEnv         = "SIMPLE_CONTAINER_AWS_LAMBDA_ROUTING_TYPE"
	lambdaRoutingTypeFunctionUrl = "function-url"
	lambdaRoutingTypeApiGw       = "api-gateway"
)

type Service interface {
	Start(ctx context.Context) error
	ProxyLambdaApiGateway(ctx context.Context, request events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error)
	ProxyLambdaFunctionURL(ctx context.Context, request events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error)
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
}

func Init(ctx context.Context, opts ...Option) error {
	log := logger.NewLogger()

	// stdout and stderr are sent to AWS CloudWatch Logs
	log.Infof(ctx, "Server cold start")

	if apiKey, err := awsutil.GetEnvOrSecret("API_KEY"); err != nil {
		log.Warnf(ctx, "Failed to get API_KEY secret: %v", err)
	} else {
		opts = append([]Option{WithApiKey(apiKey)}, opts...)
	}

	opts = append([]Option{WithVersion(os.Getenv("SIMPLE_CONTAINER_VERSION"))}, opts...)

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

	r := &service{
		ctx: ctx,
	}

	for _, opt := range opts {
		opt(r)
	}

	r.skipAuthRoutes = append(r.skipAuthRoutes, "/api/status")

	router := gin.New()
	if r.registerRoutesCallback == nil {
		return errors.Errorf("register routes callback is not set")
	}
	router.Use(gin.Recovery())
	router.Use(r.requestUIDMiddleware())
	router.Use(r.debugLogMiddleware())
	if r.apiKey != "" {
		router.Use(r.apiKeyAuthMiddleware())
	}
	router.GET("/api/status", r.statusEndpoint)

	if err := r.registerRoutesCallback(router); err != nil {
		return errors.Wrapf(err, "failed to register routes")
	}

	r.router = router

	r.lambdaAdapter = ginadapter.New(router)
	r.server = &http.Server{
		Addr:    fmt.Sprintf("0.0.0.0:%s", lo.If(r.port != "", r.port).Else("8080")),
		Handler: r.router,
	}

	ctx, cancel := context.WithCancel(ctx)
	r.cancels = append(r.cancels, cancel)
	r.ctx = ctx

	r.logger = log

	ginProxy = r

	if r.localDebugMode {
		if err := StartService(ctx); err != nil {
			return err
		}
	} else {
		routingType := os.Getenv(lambdaRoutingTypeEnv)
		switch routingType {
		case lambdaRoutingTypeFunctionUrl:
			lambda.Start(HandlerFunctionURL)
		case lambdaRoutingTypeApiGw:
			lambda.Start(HandlerAPIGateway)
		default:
			return errors.Errorf("Unknown routing type: %q \n", routingType)
		}
	}
	return nil
}

func StartService(ctx context.Context) error {
	return ginProxy.Start(ctx)
}

func HandlerFunctionURL(ctx context.Context, request events.LambdaFunctionURLRequest) (response events.LambdaFunctionURLResponse, err error) {
	return ginProxy.ProxyLambdaFunctionURL(ctx, request)
}

func HandlerAPIGateway(ctx context.Context, request events.APIGatewayProxyRequest) (response events.APIGatewayProxyResponse, err error) {
	return ginProxy.ProxyLambdaApiGateway(ctx, request)
}

func (s *service) Start(ctx context.Context) error {
	return s.server.ListenAndServe()
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
