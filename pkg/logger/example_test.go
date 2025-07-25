package logger_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/simple-container-com/go-aws-lambda-sdk/pkg/logger"
)

// Example demonstrates basic multi-sink logger usage
func ExampleLogger_basic() {
	// Create logger without console sink to avoid output in test
	var buf bytes.Buffer
	writerSink := logger.WriterSink{Writer: &buf}
	log := logger.NewLoggerWithSinks(writerSink)

	ctx := context.Background()
	log.Infof(ctx, "This goes to buffer")

	// Check that message was written
	output := buf.String()
	if strings.Contains(output, "This goes to buffer") && strings.Contains(output, `"level":"INFO"`) {
		fmt.Println("Message successfully logged to buffer")
	}
	// Output: Message successfully logged to buffer
}

// Example demonstrates file logging with rotation
func ExampleFileSink() {
	tempDir := os.TempDir()
	logFile := filepath.Join(tempDir, "app.log")

	// Create file sink
	fileSink, err := logger.NewFileSink(logFile)
	if err != nil {
		panic(err)
	}
	defer fileSink.Close()

	// Create logger with file sink
	log := logger.NewLoggerWithSinks(fileSink)

	ctx := context.Background()
	log.Infof(ctx, "Application started")
	log.Errorf(ctx, "An error occurred: %s", "database connection failed")

	fmt.Println("Logs written to file successfully")
	// Output: Logs written to file successfully
}

// Example demonstrates rotating file sink
func ExampleRotatingFileSink() {
	tempDir := os.TempDir()
	logFile := filepath.Join(tempDir, "rotating.log")

	// Create rotating file sink: max 1KB per file, keep 5 files
	rotatingSink, err := logger.NewRotatingFileSink(logFile, 1024, 5)
	if err != nil {
		panic(err)
	}
	defer rotatingSink.Close()

	log := logger.NewLoggerWithSinks(rotatingSink)

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		log.Infof(ctx, "Log message %d with some content to fill up the file", i)
	}

	fmt.Println("Logs written with rotation successfully")
	// Output: Logs written with rotation successfully
}

// Example demonstrates buffered logging for performance
func ExampleBufferedSink() {
	var buf bytes.Buffer
	writerSink := logger.WriterSink{Writer: &buf}

	// Buffer up to 10 messages, flush every 5 seconds
	bufferedSink := logger.NewBufferedSink(writerSink, 10, 5*time.Second)

	log := logger.NewLoggerWithSinks(bufferedSink)

	ctx := context.Background()

	// These messages will be buffered
	log.Infof(ctx, "Message 1")
	log.Infof(ctx, "Message 2")
	log.Infof(ctx, "Message 3")

	// Force flush
	bufferedSink.Flush()

	// Check that messages were written
	output := buf.String()
	messageCount := strings.Count(output, "Message")
	fmt.Printf("Buffered %d messages successfully\n", messageCount)
	// Output: Buffered 3 messages successfully
}

// Example demonstrates filtered logging
func ExampleFilterSink() {
	var buf bytes.Buffer
	writerSink := logger.WriterSink{Writer: &buf}

	// Only log ERROR messages
	filterSink := logger.NewFilterSink(writerSink, logger.Error)

	log := logger.NewLoggerWithSinks(filterSink)

	ctx := context.Background()
	log.Infof(ctx, "This info message will be filtered out")
	log.Warnf(ctx, "This warning will be filtered out")
	log.Errorf(ctx, "This error message will pass through")

	// Check that only error message passed through
	output := buf.String()
	if strings.Contains(output, "error message") && !strings.Contains(output, "info message") {
		fmt.Println("Filter sink working correctly")
	}
	// Output: Filter sink working correctly
}

// Example demonstrates complex multi-sink setup
func ExampleLogger_multiSink() {
	// Create multiple sinks
	var errorBuf, allLogsBuf bytes.Buffer

	// Error-only sink to separate buffer
	errorSink := logger.NewFilterSink(
		logger.WriterSink{Writer: &errorBuf},
		logger.Error,
	)

	// All logs to another buffer
	allLogsSink := logger.WriterSink{Writer: &allLogsBuf}

	// Create logger with multiple sinks
	log := logger.NewLoggerWithSinks(errorSink, allLogsSink)

	ctx := context.Background()
	ctx = log.WithValue(ctx, "requestId", "req-123")
	ctx = log.WithValue(ctx, "userId", "user-456")

	log.Infof(ctx, "User logged in successfully")
	log.Warnf(ctx, "User attempted invalid action")
	log.Errorf(ctx, "Database connection failed")

	errorLines := strings.Count(errorBuf.String(), "\n")
	allLines := strings.Count(allLogsBuf.String(), "\n")

	fmt.Printf("Error logs only: %d lines\n", errorLines)
	fmt.Printf("All logs: %d lines\n", allLines)
	// Output: Error logs only: 1 lines
	// All logs: 3 lines
}

// Example demonstrates dynamic sink management
func ExampleLogger_dynamicSinks() {
	// Start with just a buffer sink to avoid console output
	var buf bytes.Buffer
	initialSink := logger.WriterSink{Writer: &buf}
	log := logger.NewLoggerWithSinks(initialSink)

	ctx := context.Background()
	log.Infof(ctx, "Initial message")

	// Add file logging during runtime
	tempDir := os.TempDir()
	logFile := filepath.Join(tempDir, "dynamic.log")

	fileSink, err := logger.NewFileSink(logFile)
	if err != nil {
		panic(err)
	}
	defer fileSink.Close()

	log.AddSink(fileSink)
	log.Infof(ctx, "Now logging to both buffer and file")

	// Remove buffer sink, keep only file
	log.RemoveSink(initialSink)
	log.Infof(ctx, "Now logging only to file")

	fmt.Printf("Current sinks: %d\n", len(log.GetSinks()))
	// Output: Current sinks: 1
}
