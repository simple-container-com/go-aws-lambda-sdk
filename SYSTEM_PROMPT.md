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

### Request-body invariant (added 2026-10-06)

`net/http`'s own server never hands a handler a nil `Request.Body` — it uses
`http.NoBody` for an empty payload. The Lambda event→request adapters do **not**
honour that: `its-felix/aws-lambda-go-http-adapter`'s `getBody` returns a bare nil
`io.Reader` for an empty payload, so a body-less POST used to reach handlers with
`Body == nil` and any unguarded `json.NewDecoder(c.Request().Body).Decode(&v)`
panicked. `awslabs/aws-lambda-go-api-proxy` (the gin path) always passes a reader,
so only the echo/streaming and vanilla paths were affected.

`normalizeRequestBody` in `pkg/service/http_adapter.go` restores the invariant at
the SDK boundary, so **no consumer needs a per-service middleware**. It is wired at
every entry point, because no single one covers them all:

- `normalizeBodyGinMiddleware()` — first in the gin chain (`service.go`), covers
  routes, consumer middleware, swagger and `NoRoute`
- `normalizeBodyEchoMiddleware()` — installed with `echoRouter.Pre(...)`, so it runs
  before routing
- guards inside `GinAdapter` / `EchoAdapter` / `ginRouter.Use` — `ginRouter.Use`
  builds its adapter via `newGinAdapter`, **not** `GinAdapter`, so it needs its own
- `withNormalizedBody(...)` — for the paths that bypass both routers:
  `WithVanillaHandler` and `yandexTriggerHandler` (which reads `r.Body` directly)

If you add a new entry point that hands a request to caller code, normalise there too.

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

## Releasing and consumer uptake (added 2026-10-06)

This repo is a **library** — no stack, no `.sc/` config, no deployable artifact, and
no isolated-stack lane. Release is fully automatic on merge to `main`
(`.github/workflows/push.yaml`): `reecetech/version-increment` computes the next
**calver** version and `welder deploy -e prod` runs `welder.yaml`'s `tag-release`
task, which tags and pushes it. No operator `git tag` step is needed.

The pushed tag (e.g. `2026.9.2`) has **no `v` prefix**, so it is not a valid Go module
version. Consumers therefore pin a **pseudo-version** instead:

```bash
go get github.com/simple-container-com/go-aws-lambda-sdk@<merge-commit-sha>
go mod tidy   # → v0.0.0-<utc-timestamp>-<12-char-sha>
```

By convention the SHA consumers pin is always a release-tagged commit on `main` —
never an unmerged branch commit. Consumer pins drift widely (four distinct
generations in the org as of 2026-10-06), so "all consumers are on version X" is never
a safe assumption: read each `go.mod`. See
`docs/rollout/nil-body-normalisation-uptake.md` for the verified inventory and the
ordered uptake plan.

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

## Known repo-level debt (observed 2026-10-06, not fixed)

- `.golangci.yml` is v1-format while current `golangci-lint` binaries are v2.x, so an
  ad-hoc `golangci-lint run` fails with `unsupported version of the configuration: ""`.
  CI is unaffected (`welder.yaml`'s `linters` task runs `bin/golangci-lint` built from
  the `go.mod`-pinned `v1.64.8`), but local/agent lint runs are a no-op — run the
  enabled linters (`gci`, `gofumpt`, `staticcheck`, `errcheck`, `ineffassign`) directly
  until the config is migrated.
- `welder.yaml`'s `tools` task runs `go get` + `go mod tidy` as part of `build`, so a
  release build can mutate `go.mod`.

## Notes for Contributors

- Always update this SYSTEM_PROMPT.md when gaining new knowledge about the project
- Follow the established patterns for middleware and adapters
- Ensure compatibility with both local and Lambda environments
- Use structured logging with context
- Write tests using the established patterns
