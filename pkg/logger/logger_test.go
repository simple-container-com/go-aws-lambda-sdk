package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLogger(t *testing.T) {
	logger := NewLogger()
	assert.NotNil(t, logger)

	// Should have default console sink
	sinks := logger.GetSinks()
	assert.Len(t, sinks, 1)
	assert.IsType(t, ConsoleSink{}, sinks[0])
}

func TestNewLoggerWithSinks(t *testing.T) {
	var buf bytes.Buffer
	writerSink := WriterSink{Writer: &buf}

	logger := NewLoggerWithSinks(writerSink)
	assert.NotNil(t, logger)

	sinks := logger.GetSinks()
	assert.Len(t, sinks, 1)
	assert.Equal(t, writerSink, sinks[0])
}

func TestAddRemoveSink(t *testing.T) {
	logger := NewLogger()

	// Add a writer sink
	var buf bytes.Buffer
	writerSink := WriterSink{Writer: &buf}
	logger.AddSink(writerSink)

	sinks := logger.GetSinks()
	assert.Len(t, sinks, 2) // Console + Writer

	// Remove the writer sink
	logger.RemoveSink(writerSink)
	sinks = logger.GetSinks()
	assert.Len(t, sinks, 1) // Only console remains
}

func TestWriterSink(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLoggerWithSinks(WriterSink{Writer: &buf})

	ctx := context.Background()
	logger.Infof(ctx, "test message %s", "arg")

	// Check that message was written to buffer
	output := buf.String()
	assert.Contains(t, output, "test message arg")
	assert.Contains(t, output, `"level":"INFO"`)

	// Verify it's valid JSON
	var msg Message
	err := json.Unmarshal([]byte(strings.TrimSpace(output)), &msg)
	assert.NoError(t, err)
	assert.Equal(t, "INFO", msg.Level)
	assert.Equal(t, "test message arg", msg.Message)
}

func TestMultipleSinks(t *testing.T) {
	var buf1, buf2 bytes.Buffer
	sink1 := WriterSink{Writer: &buf1}
	sink2 := WriterSink{Writer: &buf2}

	logger := NewLoggerWithSinks(sink1, sink2)

	ctx := context.Background()
	logger.Errorf(ctx, "error message")

	// Both sinks should receive the message
	output1 := buf1.String()
	output2 := buf2.String()

	assert.Contains(t, output1, "error message")
	assert.Contains(t, output1, `"level":"ERROR"`)
	assert.Contains(t, output2, "error message")
	assert.Contains(t, output2, `"level":"ERROR"`)
}

func TestContextValues(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLoggerWithSinks(WriterSink{Writer: &buf})

	ctx := context.Background()
	ctx = logger.WithValue(ctx, "key1", "value1")
	ctx = logger.WithValues(ctx, map[string]any{
		"key2": "value2",
		"key3": 123,
	})

	logger.Infof(ctx, "test with context")

	output := buf.String()
	var msg Message
	err := json.Unmarshal([]byte(strings.TrimSpace(output)), &msg)
	require.NoError(t, err)

	assert.Equal(t, "value1", msg.Context["key1"])
	assert.Equal(t, "value2", msg.Context["key2"])
	assert.Equal(t, float64(123), msg.Context["key3"]) // JSON unmarshals numbers as float64
}

func TestGetValue(t *testing.T) {
	logger := NewLogger()
	ctx := context.Background()

	// Test with no value
	assert.Nil(t, logger.GetValue(ctx, "nonexistent"))

	// Test with value
	ctx = logger.WithValue(ctx, "testkey", "testvalue")
	assert.Equal(t, "testvalue", logger.GetValue(ctx, "testkey"))
}

func TestFileSink(t *testing.T) {
	tempDir := t.TempDir()
	logFile := filepath.Join(tempDir, "test.log")

	fileSink, err := NewFileSink(logFile)
	require.NoError(t, err)
	defer fileSink.Close()

	logger := NewLoggerWithSinks(fileSink)

	ctx := context.Background()
	logger.Infof(ctx, "file test message")

	// Read the file content
	content, err := os.ReadFile(logFile)
	require.NoError(t, err)

	assert.Contains(t, string(content), "file test message")
	assert.Contains(t, string(content), `"level":"INFO"`)
}

func TestRotatingFileSink(t *testing.T) {
	tempDir := t.TempDir()
	logFile := filepath.Join(tempDir, "rotating.log")

	// Create a rotating sink with very small max size to trigger rotation
	rotatingSink, err := NewRotatingFileSink(logFile, 100, 3)
	require.NoError(t, err)
	defer rotatingSink.Close()

	logger := NewLoggerWithSinks(rotatingSink)
	ctx := context.Background()

	// Write enough messages to trigger rotation
	for i := 0; i < 10; i++ {
		logger.Infof(ctx, "rotating test message number %d with some extra content to make it longer", i)
	}

	// Check that rotation occurred
	_, err = os.Stat(logFile + ".1")
	assert.NoError(t, err, "Rotated file should exist")
}

func TestBufferedSink(t *testing.T) {
	var buf bytes.Buffer
	writerSink := WriterSink{Writer: &buf}
	bufferedSink := NewBufferedSink(writerSink, 3, 100*time.Millisecond)

	logger := NewLoggerWithSinks(bufferedSink)
	ctx := context.Background()

	// Write 2 messages (less than buffer size)
	logger.Infof(ctx, "message 1")
	logger.Infof(ctx, "message 2")

	// Should not be written yet
	assert.Empty(t, buf.String())

	// Write third message to trigger flush
	logger.Infof(ctx, "message 3")

	// Now all messages should be written
	output := buf.String()
	assert.Contains(t, output, "message 1")
	assert.Contains(t, output, "message 2")
	assert.Contains(t, output, "message 3")
}

func TestFilterSink(t *testing.T) {
	var buf bytes.Buffer
	writerSink := WriterSink{Writer: &buf}
	// Only allow ERROR messages
	filterSink := NewFilterSink(writerSink, Error)

	logger := NewLoggerWithSinks(filterSink)
	ctx := context.Background()

	logger.Infof(ctx, "info message")   // Should be filtered out
	logger.Warnf(ctx, "warn message")   // Should be filtered out
	logger.Errorf(ctx, "error message") // Should pass through

	output := buf.String()
	assert.NotContains(t, output, "info message")
	assert.NotContains(t, output, "warn message")
	assert.Contains(t, output, "error message")
}

func TestSinkError(t *testing.T) {
	// Create a sink that always fails
	failingSink := &failingSink{}
	logger := NewLoggerWithSinks(failingSink)

	ctx := context.Background()
	// This should not panic, even though the sink fails
	logger.Infof(ctx, "test message")
}

// Helper sink that always returns an error
type failingSink struct{}

func (s *failingSink) Write(msg Message) error {
	return assert.AnError
}
