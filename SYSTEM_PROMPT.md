# Go AWS Lambda SDK for simple-container.com

## Project Overview

This is a Go SDK designed to simplify the development and deployment of AWS Lambda functions for the simple-container.com platform. The SDK provides a unified interface for building HTTP services that can run both locally for development and as AWS Lambda functions in production.

## Architecture

### Core Components

1. **Service Package (`pkg/service/`)** - The main service framework
   - `service.go` - Core service interface and implementation
   - `http_adapter.go` - HTTP adapter abstraction for different frameworks (Gin, Echo)
   - `router.go` - Request routing and middleware
   - `options.go` - Service configuration options
   - `helpers.go` - Utility functions for request/response handling

2. **Logger Package (`pkg/logger/`)** - Structured logging with multi-sink support
   - Context-aware logging with JSON output
   - Supports Info, Warn, and Error levels
   - **Multi-sink architecture**: Can output to multiple destinations simultaneously
   - **Built-in sinks**: Console, File, Rotating File, Buffered, Filter, Writer sinks
   - Designed for AWS CloudWatch integration
   - Dynamic sink management (add/remove sinks at runtime)

3. **AWS Utilities (`pkg/awsutil/`)** - AWS-specific utilities
   - `lambda.go` - Lambda event transformations between API Gateway and Function URL formats
   - `secrets.go` - AWS Secrets Manager integration

4. **Utilities (`pkg/util/`)** - General utility functions
   - `split.go` - String splitting utilities
   - `maps/` - Map manipulation functions
   - `retry/` - Retry logic utilities

### Key Features

- **Dual Runtime Support**: Can run as a local HTTP server for development or as AWS Lambda function
- **Framework Agnostic**: Supports both Gin and Echo web frameworks through adapters
- **Lambda Routing Types**: Supports both API Gateway and Function URL routing
- **Built-in Middleware**: Request logging, authentication, CORS handling
- **Swagger Integration**: Built-in support for API documentation
- **Response Streaming**: Optional streaming response support for Lambda Function URLs

## Environment Variables

- `SIMPLE_CONTAINER_VERSION` - Service version
- `SIMPLE_CONTAINER_AWS_LAMBDA_ROUTING_TYPE` - Lambda routing type (`function-url` or `api-gateway`)
- `SIMPLE_CONTAINER_AWS_LAMBDA_SIZE_MB` - Lambda memory size
- `LOCAL_DEBUG` - Enable local development mode
- `REQUEST_DEBUG` - Enable request debugging
- `API_KEY` - API key for authentication

## Development Workflow

### Local Development
- Set `LOCAL_DEBUG=true` to run as local HTTP server
- Use `welder.yaml` configuration for build and deployment
- Run `go run ./cmd/go-aws-lambda-sdk` for local testing

### Production Deployment
- Service automatically detects Lambda environment
- Supports both API Gateway and Function URL routing
- Integrates with AWS CloudWatch for logging

## Build System

- **Welder**: Uses `welder.yaml` for build configuration
- **Tools**: Managed through `tools.go` with code generation
- **Linting**: golangci-lint configuration in `.golangci.yml`
- **Dependencies**: Go modules with extensive AWS and web framework dependencies

## Code Organization Principles

1. **Interface-Based Design**: Core components use interfaces for testability
2. **Context Propagation**: Consistent use of Go context for request lifecycle
3. **Error Handling**: Structured error handling with pkg/errors
4. **Configuration**: Options pattern for service configuration
5. **Middleware Pattern**: Composable request processing pipeline

## Testing Strategy

- Uses testify for testing framework
- Mockery for generating mocks
- Supports both unit and integration testing patterns

## Key Dependencies

- **AWS SDK**: `aws-lambda-go`, `aws-secretsmanager-caching-go`
- **Web Frameworks**: Gin, Echo with Lambda adapters
- **Utilities**: `samber/lo` for functional programming, `google/uuid`
- **Documentation**: Swagger/OpenAPI integration
- **Development Tools**: golangci-lint, gofumpt, mockery

## Usage Pattern

```go
// Create service with options
service, err := service.New(ctx,
    service.WithVersion("1.0.0"),
    service.WithRegisterRoutesCallback(registerRoutes),
    service.WithApiKey("your-api-key"),
)

// Start service (detects environment automatically)
err = service.Start()
```

## Logger Usage Patterns

The logger supports multiple output destinations (sinks) that can be configured dynamically:

### Basic Usage
```go
// Default logger with console output
logger := logger.NewLogger()

// Logger with custom sinks
logger := logger.NewLoggerWithSinks(
    logger.ConsoleSink{},
    fileSink,
    bufferedSink,
)
```

### Available Sinks
- **ConsoleSink**: Outputs to stdout/stderr (default)
- **FileSink**: Writes to a single file
- **RotatingFileSink**: Writes to files with size-based rotation
- **BufferedSink**: Buffers messages for performance
- **FilterSink**: Filters messages by log level
- **WriterSink**: Writes to any io.Writer

### Dynamic Sink Management
```go
// Add sinks at runtime
logger.AddSink(newFileSink)

// Remove specific sinks
logger.RemoveSink(oldSink)

// Get current sinks
sinks := logger.GetSinks()
```

### Advanced Configuration
```go
// File logging with rotation
rotatingSink, _ := logger.NewRotatingFileSink("app.log", 10*1024*1024, 5)

// Buffered logging for high-performance scenarios
bufferedSink := logger.NewBufferedSink(fileSink, 100, 5*time.Second)

// Error-only logging to separate destination
errorSink := logger.NewFilterSink(fileSink, logger.Error)

// Combine multiple sinks
logger := logger.NewLoggerWithSinks(
    logger.ConsoleSink{},
    rotatingSink,
    errorSink,
)
```

## Notes for Contributors

- Always update this SYSTEM_PROMPT.md when gaining new knowledge about the project
- Follow the established patterns for middleware and adapters
- Ensure compatibility with both local and Lambda environments
- Use structured logging with context
- Write tests using the established patterns
