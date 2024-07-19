package logger

import (
	"context"
	"encoding/json"
	"fmt"
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

type Logger interface {
	Infof(ctx context.Context, format string, args ...any)
	Errorf(ctx context.Context, format string, args ...any)
	Warnf(ctx context.Context, format string, args ...any)
	WithValue(ctx context.Context, key string, value any) context.Context
	WithValues(ctx context.Context, values map[string]any) context.Context
	GetValue(ctx context.Context, key string) any
}

type logger struct{}

type Message struct {
	Date    string       `json:"date"`
	Level   string       `json:"level"`
	Message string       `json:"message"`
	Context ContextValue `json:"context"`
}

func NewLogger() Logger {
	return &logger{}
}

func (l logger) GetValue(ctx context.Context, key string) any {
	ctxValueOrNil := ctx.Value(contextValueKey)
	if ctxValueOrNil == nil {
		return nil
	}
	return ctxValueOrNil.(ContextValue)[key]
}

func (l logger) WithValues(ctx context.Context, values map[string]any) context.Context {
	for k, v := range values {
		ctx = l.WithValue(ctx, k, v)
	}
	return ctx
}

func (l logger) WithValue(ctx context.Context, key string, value any) context.Context {
	currentValue, ok := ctx.Value(contextValueKey).(ContextValue)
	if ok {
		newValue := lo.Assign(currentValue)
		newValue[key] = value
		return context.WithValue(ctx, contextValueKey, newValue)
	}
	return context.WithValue(ctx, contextValueKey, ContextValue{key: value})
}

func (l logger) Infof(ctx context.Context, format string, args ...any) {
	l.printWithLevel(ctx, format, args, Info)
}

func (l logger) Warnf(ctx context.Context, format string, args ...any) {
	l.printWithLevel(ctx, format, args, Warn)
}

func (l logger) Errorf(ctx context.Context, format string, args ...any) {
	l.printWithLevel(ctx, format, args, Error)
}

func (l logger) printWithLevel(ctx context.Context, format string, args []any, level string) {
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
	jsonOutput, err := json.Marshal(msg)
	printer := os.Stdout
	if level == Error {
		printer = os.Stderr
	}
	if err != nil {
		_, _ = printer.WriteString(fmt.Sprintf(`{"level":"%s","message":"%s","context":{"error":"%s"}}`, level, message, err.Error()) + "\n")
	}
	_, _ = printer.WriteString(string(jsonOutput) + "\n")
}
