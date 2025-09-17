package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/samber/lo"
)

type ContextValue map[string]any

type contextValueKeyType struct{}

var contextValueKey contextValueKeyType = struct{}{}

const (
	Info  = "INFO"
	Error = "ERROR"
	Warn  = "WARN"
)

// Sink represents a log output destination
type Sink interface {
	Write(msg Message) error
}

// ConsoleSink writes logs to stdout/stderr
type ConsoleSink struct{}

func (s ConsoleSink) Write(msg Message) error {
	jsonOutput, err := json.Marshal(msg)
	printer := os.Stdout
	if msg.Level == Error {
		printer = os.Stderr
	}
	if err != nil {
		_, writeErr := printer.WriteString(fmt.Sprintf(`{"level":"%s","message":"%s","context":{"error":"%s"}}`, msg.Level, msg.Message, err.Error()) + "\n")
		return writeErr
	}
	_, writeErr := printer.WriteString(string(jsonOutput) + "\n")
	return writeErr
}

// WriterSink writes logs to any io.Writer
type WriterSink struct {
	Writer io.Writer
}

func (s WriterSink) Write(msg Message) error {
	jsonOutput, err := json.Marshal(msg)
	if err != nil {
		_, writeErr := s.Writer.Write([]byte(fmt.Sprintf(`{"level":"%s","message":"%s","context":{"error":"%s"}}`, msg.Level, msg.Message, err.Error()) + "\n"))
		return writeErr
	}
	_, writeErr := s.Writer.Write(append(jsonOutput, '\n'))
	return writeErr
}

type Logger interface {
	Infof(ctx context.Context, format string, args ...any)
	Errorf(ctx context.Context, format string, args ...any)
	Warnf(ctx context.Context, format string, args ...any)
	WithValue(ctx context.Context, key string, value any) context.Context
	WithValues(ctx context.Context, values map[string]any) context.Context
	GetValue(ctx context.Context, key string) any
	// New methods for sink management
	AddSink(sink Sink)
	RemoveSink(sink Sink)
	GetSinks() []Sink
}

type logger struct {
	sinks []Sink
}

type Message struct {
	Date    string       `json:"date"`
	Level   string       `json:"level"`
	Message string       `json:"message"`
	Context ContextValue `json:"context"`
}

func NewLogger() Logger {
	return &logger{
		sinks: []Sink{ConsoleSink{}}, // Default to console output for backward compatibility
	}
}

func NewLoggerWithSinks(sinks ...Sink) Logger {
	return &logger{
		sinks: sinks,
	}
}

func (l *logger) AddSink(sink Sink) {
	l.sinks = append(l.sinks, sink)
}

func (l *logger) RemoveSink(sink Sink) {
	l.sinks = lo.Filter(l.sinks, func(s Sink, _ int) bool {
		return s != sink
	})
}

func (l *logger) GetSinks() []Sink {
	return l.sinks
}

func (l *logger) GetValue(ctx context.Context, key string) any {
	ctxValueOrNil := ctx.Value(contextValueKey)
	if ctxValueOrNil == nil {
		return nil
	}
	return ctxValueOrNil.(ContextValue)[key]
}

func (l *logger) WithValues(ctx context.Context, values map[string]any) context.Context {
	for k, v := range values {
		ctx = l.WithValue(ctx, k, v)
	}
	return ctx
}

func (l *logger) WithValue(ctx context.Context, key string, value any) context.Context {
	currentValue, ok := ctx.Value(contextValueKey).(ContextValue)
	if ok {
		newValue := lo.Assign(currentValue)
		newValue[key] = value
		return context.WithValue(ctx, contextValueKey, newValue)
	}
	return context.WithValue(ctx, contextValueKey, ContextValue{key: value})
}

func (l *logger) Infof(ctx context.Context, format string, args ...any) {
	l.printWithLevel(ctx, format, args, Info)
}

func (l *logger) Warnf(ctx context.Context, format string, args ...any) {
	l.printWithLevel(ctx, format, args, Warn)
}

func (l *logger) Errorf(ctx context.Context, format string, args ...any) {
	l.printWithLevel(ctx, format, args, Error)
}

func (l *logger) printWithLevel(ctx context.Context, format string, args []any, level string) {
	ctxValueOrNil := ctx.Value(contextValueKey)
	contextValue := ContextValue{}
	if ctxValueOrNil != nil {
		contextValue = ctxValueOrNil.(ContextValue)
	}
	message := fmt.Sprintf(format, args...)
	msg := Message{
		Date:    time.Now().Format(time.DateTime),
		Level:   level,
		Message: message,
		Context: contextValue,
	}

	// Write to all registered sinks
	for _, sink := range l.sinks {
		if err := sink.Write(msg); err != nil {
			// If writing to a sink fails, write error to stderr as fallback
			err2 := l.sinks[0].Write(Message{
				Date:    time.Now().Format(time.DateTime),
				Level:   Error,
				Message: "Logger sink error",
				Context: ContextValue{"error": err.Error()},
			})
			if err2 != nil {
				_, _ = fmt.Fprintf(os.Stderr, "Logger sink error: %v\n", err)
			}
		}
	}
}
