package logger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileSink writes logs to a file with optional rotation
type FileSink struct {
	filepath string
	file     *os.File
	mutex    sync.Mutex
}

func NewFileSink(filepath string) (*FileSink, error) {
	// Ensure directory exists
	dir := filepath[:len(filepath)-len(filepath[len(filepath)-1:])]
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create log directory: %w", err)
		}
	}

	file, err := os.OpenFile(filepath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	return &FileSink{
		filepath: filepath,
		file:     file,
	}, nil
}

func (s *FileSink) Write(msg Message) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	jsonOutput, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal log message: %w", err)
	}

	if _, err := s.file.Write(append(jsonOutput, '\n')); err != nil {
		return fmt.Errorf("failed to write to log file: %w", err)
	}

	return s.file.Sync()
}

func (s *FileSink) Close() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.file != nil {
		return s.file.Close()
	}
	return nil
}

// RotatingFileSink writes logs to files with size-based rotation
type RotatingFileSink struct {
	basePath    string
	maxSize     int64 // in bytes
	maxFiles    int
	currentFile *os.File
	currentSize int64
	mutex       sync.Mutex
}

func NewRotatingFileSink(basePath string, maxSize int64, maxFiles int) (*RotatingFileSink, error) {
	sink := &RotatingFileSink{
		basePath: basePath,
		maxSize:  maxSize,
		maxFiles: maxFiles,
	}

	if err := sink.openCurrentFile(); err != nil {
		return nil, err
	}

	return sink, nil
}

func (s *RotatingFileSink) openCurrentFile() error {
	// Ensure directory exists
	dir := filepath.Dir(s.basePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	file, err := os.OpenFile(s.basePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return fmt.Errorf("failed to stat log file: %w", err)
	}

	s.currentFile = file
	s.currentSize = stat.Size()
	return nil
}

func (s *RotatingFileSink) rotate() error {
	if s.currentFile != nil {
		if err := s.currentFile.Close(); err != nil {
			return fmt.Errorf("failed to close current log file: %w", err)
		}
	}

	// Rotate existing files
	for i := s.maxFiles - 1; i > 0; i-- {
		oldPath := fmt.Sprintf("%s.%d", s.basePath, i)
		newPath := fmt.Sprintf("%s.%d", s.basePath, i+1)

		if i == s.maxFiles-1 {
			// Remove the oldest file
			if err := os.Remove(newPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to remove oldest log file %s: %w", newPath, err)
			}
		}

		if _, err := os.Stat(oldPath); err == nil {
			if err := os.Rename(oldPath, newPath); err != nil {
				return fmt.Errorf("failed to rotate log file from %s to %s: %w", oldPath, newPath, err)
			}
		}
	}

	// Move current file to .1
	if _, err := os.Stat(s.basePath); err == nil {
		if err := os.Rename(s.basePath, s.basePath+".1"); err != nil {
			return fmt.Errorf("failed to rotate current log file %s: %w", s.basePath, err)
		}
	}

	return s.openCurrentFile()
}

func (s *RotatingFileSink) Write(msg Message) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	jsonOutput, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal log message: %w", err)
	}

	logLine := append(jsonOutput, '\n')

	// Check if rotation is needed
	if s.currentSize+int64(len(logLine)) > s.maxSize {
		if err := s.rotate(); err != nil {
			return fmt.Errorf("failed to rotate log file: %w", err)
		}
	}

	if _, err := s.currentFile.Write(logLine); err != nil {
		return fmt.Errorf("failed to write to log file: %w", err)
	}

	s.currentSize += int64(len(logLine))
	return s.currentFile.Sync()
}

func (s *RotatingFileSink) Close() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.currentFile != nil {
		return s.currentFile.Close()
	}
	return nil
}

// BufferedSink buffers log messages and flushes them periodically or when buffer is full
type BufferedSink struct {
	sink       Sink
	buffer     []Message
	bufferSize int
	flushTimer *time.Timer
	flushDelay time.Duration
	mutex      sync.Mutex
}

func NewBufferedSink(sink Sink, bufferSize int, flushDelay time.Duration) *BufferedSink {
	return &BufferedSink{
		sink:       sink,
		buffer:     make([]Message, 0, bufferSize),
		bufferSize: bufferSize,
		flushDelay: flushDelay,
	}
}

func (s *BufferedSink) Write(msg Message) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.buffer = append(s.buffer, msg)

	// Reset flush timer
	if s.flushTimer != nil {
		s.flushTimer.Stop()
	}
	s.flushTimer = time.AfterFunc(s.flushDelay, func() {
		s.Flush()
	})

	// Flush if buffer is full
	if len(s.buffer) >= s.bufferSize {
		return s.flush()
	}

	return nil
}

func (s *BufferedSink) Flush() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.flush()
}

func (s *BufferedSink) flush() error {
	if len(s.buffer) == 0 {
		return nil
	}

	// Write all buffered messages
	for _, msg := range s.buffer {
		if err := s.sink.Write(msg); err != nil {
			return fmt.Errorf("failed to write buffered message: %w", err)
		}
	}

	// Clear buffer
	s.buffer = s.buffer[:0]

	if s.flushTimer != nil {
		s.flushTimer.Stop()
		s.flushTimer = nil
	}

	return nil
}

// FilterSink filters messages based on level before passing to underlying sink
type FilterSink struct {
	sink   Sink
	levels map[string]bool
}

func NewFilterSink(sink Sink, levels ...string) *FilterSink {
	levelMap := make(map[string]bool)
	for _, level := range levels {
		levelMap[level] = true
	}
	return &FilterSink{
		sink:   sink,
		levels: levelMap,
	}
}

func (s *FilterSink) Write(msg Message) error {
	if s.levels[msg.Level] {
		return s.sink.Write(msg)
	}
	return nil
}

// ObservatorySink sends logs to an observatory service
type ObservatorySink struct {
	baseUri string
}

func NewObservatorySink(baseUri string) *ObservatorySink {
	return &ObservatorySink{
		baseUri: baseUri,
	}
}

func (s *ObservatorySink) Write(msg Message) error {
	body := ObservatoryPushLogsRequest{
		Logs: []ObservatoryPushLogsRequestLog{
			{
				Message:   msg.Message,
				LogLevel:  msg.Level,
				Data:      msg.Context,
				Module:    nil,
				Submodule: nil,
			},
		},
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("failed to marshal observatory log message: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/observatory/logs", s.baseUri)
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBody))
	if err != nil {
		return fmt.Errorf("failed to send observatory log message: %w", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("observatory log request failed with status: %s", resp.Status)
	}
	return nil
}

type ObservatoryPushLogsRequest struct {
	Logs []ObservatoryPushLogsRequestLog `json:"logs"`
}

type ObservatoryPushLogsRequestLog struct {
	Message   string  `json:"message"`
	LogLevel  string  `json:"logLevel"`
	Data      any     `json:"data"`
	Module    *string `json:"module"`
	Submodule *string `json:"submodule"`
}
