package reef

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestColorizeFieldValues(t *testing.T) {
	tests := []struct {
		name        string
		handler     *Handler
		input       string
		level       slog.Level
		expected    []string // strings that should be present in output
		notExpected []string // strings that should not be present
	}{
		{
			name: "basic key value coloring",
			handler: NewHandler(
				WithColors(),
				WithoutTimestamp(),
			),
			input:    "msg=hello user=john",
			level:    slog.LevelInfo,
			expected: []string{"\033[2m", "\033[22m", "\033[0m"}, // dim, brighten, reset codes
		},
		{
			name: "custom key colors",
			handler: NewHandler(
				WithColors(),
				WithKeyColor("user", "\033[95m"), // bright magenta
				WithoutTimestamp(),
			),
			input:    "msg=hello user=john",
			level:    slog.LevelInfo,
			expected: []string{"\033[95m", "user=", "john"},
		},
		{
			name: "level colors with line coloring",
			handler: NewHandler(
				WithColors(),
				WithLevelLineColoring(),
				WithoutTimestamp(),
			),
			input:    "msg=error level=ERROR",
			level:    slog.LevelError,
			expected: []string{"\033[31m"}, // red for error level
		},
		{
			name: "colors disabled",
			handler: NewHandler(
				WithoutColors(),
				WithoutTimestamp(),
			),
			input:       "msg=hello user=john",
			level:       slog.LevelInfo,
			notExpected: []string{"\033[", "\033[2m", "\033[22m", "\033[0m"},
		},
		{
			name: "quoted values handling",
			handler: NewHandler(
				WithColors(),
				WithoutTimestamp(),
			),
			input:    `msg="hello world" user="john doe"`,
			level:    slog.LevelInfo,
			expected: []string{"\033[2m", "\033[22m", `"hello world"`, `"john doe"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.handler.colorizeFieldValues(tt.input, tt.level, "")

			for _, expected := range tt.expected {
				if !strings.Contains(result, expected) {
					t.Errorf("Expected output to contain %q, got: %s", expected, result)
				}
			}

			for _, notExpected := range tt.notExpected {
				if strings.Contains(result, notExpected) {
					t.Errorf("Expected output to NOT contain %q, got: %s", notExpected, result)
				}
			}
		})
	}
}

func TestHandlerIntegration(t *testing.T) {
	tests := []struct {
		name     string
		setup    func() (*Handler, *bytes.Buffer)
		logFunc  func(logger *slog.Logger)
		validate func(t *testing.T, output string)
	}{
		{
			name: "text handler with colors",
			setup: func() (*Handler, *bytes.Buffer) {
				buf := &bytes.Buffer{}
				handler := NewHandler(
					WithWriter(buf),
					WithColors(),
					WithoutTimestamp(),
				)
				return handler, buf
			},
			logFunc: func(logger *slog.Logger) {
				logger.Info("test message", "key1", "value1", "key2", "value2")
			},
			validate: func(t *testing.T, output string) {
				if !strings.Contains(output, "test message") {
					t.Errorf("Expected message not found in output: %q", output)
				}
				if !strings.Contains(output, "key1") {
					t.Errorf("Expected key1 not found in output: %q", output)
				}
				if !strings.Contains(output, "\033[2m") { // dim color
					t.Errorf("Expected color codes not found in output: %q", output)
				}
			},
		},
		{
			name: "json handler ignores colors",
			setup: func() (*Handler, *bytes.Buffer) {
				buf := &bytes.Buffer{}
				handler := NewHandler(
					WithHandlerType(JSONHandler),
					WithWriter(buf),
					WithColors(),
					WithoutTimestamp(),
				)
				return handler, buf
			},
			logFunc: func(logger *slog.Logger) {
				logger.Info("test message", "key1", "value1")
			},
			validate: func(t *testing.T, output string) {
				if strings.Contains(output, "\033[") {
					t.Error("JSON handler should not contain ANSI color codes")
				}
				if !strings.Contains(output, `"msg":"test message"`) {
					t.Error("Expected JSON format not found")
				}
			},
		},
		{
			name: "custom key colors",
			setup: func() (*Handler, *bytes.Buffer) {
				buf := &bytes.Buffer{}
				handler := NewHandler(
					WithWriter(buf),
					WithColors(),
					WithKeyColors(map[string]string{
						"database": "\033[95m", // bright magenta
						"version":  "\033[96m", // bright cyan
					}),
					WithoutTimestamp(),
				)
				return handler, buf
			},
			logFunc: func(logger *slog.Logger) {
				logger.Info("app started", "database", "postgres", "version", "1.0.0")
			},
			validate: func(t *testing.T, output string) {
				if !strings.Contains(output, "\033[95m") { // bright magenta
					t.Error("Expected custom database color not found")
				}
				if !strings.Contains(output, "\033[96m") { // bright cyan
					t.Error("Expected custom version color not found")
				}
			},
		},
		{
			name: "level line coloring",
			setup: func() (*Handler, *bytes.Buffer) {
				buf := &bytes.Buffer{}
				handler := NewHandler(
					WithWriter(buf),
					WithColors(),
					WithLevelLineColoring(),
					WithoutTimestamp(),
				)
				return handler, buf
			},
			logFunc: func(logger *slog.Logger) {
				logger.Error("error message", "code", 500)
			},
			validate: func(t *testing.T, output string) {
				if !strings.Contains(output, "\033[31m") { // red for error
					t.Error("Expected red color for error level not found")
				}
			},
		},
		{
			name: "dynamic line color attribute",
			setup: func() (*Handler, *bytes.Buffer) {
				buf := &bytes.Buffer{}
				handler := NewHandler(
					WithWriter(buf),
					WithColors(),
					WithColorAttrKey("_c"),
					WithoutTimestamp(),
				)
				return handler, buf
			},
			logFunc: func(logger *slog.Logger) {
				logger.Info("colored message", "_c", "red", "user", "john")
			},
			validate: func(t *testing.T, output string) {
				if !strings.Contains(output, "\033[31m") { // red color
					t.Error("Expected red color from _c attribute not found")
				}
				// The _c attribute should be removed from output
				if strings.Contains(output, "_c=red") {
					t.Error("Color attribute should be removed from output")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, buf := tt.setup()
			logger := slog.New(handler)

			tt.logFunc(logger)

			output := buf.String()
			tt.validate(t, output)
		})
	}
}

func TestColorNameMapping(t *testing.T) {
	tests := []struct {
		colorName    string
		expectedCode string
	}{
		{"red", "\033[31m"},
		{"green", "\033[32m"},
		{"blue", "\033[34m"},
		{"bright_red", "\033[91m"},
		{"bg_yellow", "\033[43m"},
	}

	handler := NewHandler()

	for _, tt := range tests {
		t.Run(tt.colorName, func(t *testing.T) {
			result := handler.parseColorValue(tt.colorName)
			if result != tt.expectedCode {
				t.Errorf("Expected %q for color %q, got %q", tt.expectedCode, tt.colorName, result)
			}
		})
	}
}

func TestParseColorValue(t *testing.T) {
	handler := NewHandler()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"ansi code passthrough", "\033[31m", "\033[31m"},
		{"color name lookup", "red", "\033[31m"},
		{"unknown color", "unknown", ""},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.parseColorValue(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestFindValueEnd(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		start    int
		expected int
	}{
		{"simple value", "key=value next", 4, 9},
		{"quoted value", `key="quoted value" next`, 4, 18},
		{"escaped quote", `key="escaped \"quote\"" next`, 4, 23},
		{"end of line", "key=value", 4, 9},
		{"empty value", "key= next", 4, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findValueEnd([]byte(tt.line), tt.start)
			if result != tt.expected {
				t.Errorf("Expected %d, got %d for line %q starting at %d", tt.expected, result, tt.line, tt.start)
			}
		})
	}
}

func TestWithAttrsColorExtraction(t *testing.T) {
	buf := &bytes.Buffer{}
	handler := NewHandler(
		WithWriter(buf),
		WithColors(),
		WithColorAttrKey("_c"),
		WithoutTimestamp(),
	)

	// Create logger with color attribute
	logger := slog.New(handler).With("_c", "blue", "component", "test")
	logger.Info("test message", "key", "value")

	output := buf.String()

	// Should contain blue color code
	if !strings.Contains(output, "\033[34m") {
		t.Error("Expected blue color code not found")
	}

	// Should not contain the _c attribute in output
	if strings.Contains(output, "_c=blue") {
		t.Error("Color attribute should be removed from output")
	}

	// Should contain other attributes
	if !strings.Contains(output, "component") {
		t.Errorf("Expected component attribute not found in output: %q", output)
	}
}

func TestTimestampHandling(t *testing.T) {
	tests := []struct {
		name     string
		handler  *Handler
		validate func(t *testing.T, output string)
	}{
		{
			name:    "remove timestamp",
			handler: NewHandler(WithoutTimestamp()),
			validate: func(t *testing.T, output string) {
				if strings.Contains(output, "time=") {
					t.Error("Timestamp should be removed from output")
				}
			},
		},
		{
			name:    "custom timestamp format",
			handler: NewHandler(WithTimestampFormat("15:04:05")),
			validate: func(t *testing.T, output string) {
				// Should contain time in HH:MM:SS format
				if !strings.Contains(output, "time=") {
					t.Error("Custom timestamp should be present")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			tt.handler.writer = buf

			logger := slog.New(tt.handler)
			logger.Info("test message")

			output := buf.String()
			tt.validate(t, output)
		})
	}
}

func TestHandlerEnabled(t *testing.T) {
	handler := NewHandler(WithLevel(slog.LevelWarn))

	ctx := context.Background()

	if handler.Enabled(ctx, slog.LevelDebug) {
		t.Error("Debug should be disabled when level is Warn")
	}

	if handler.Enabled(ctx, slog.LevelInfo) {
		t.Error("Info should be disabled when level is Warn")
	}

	if !handler.Enabled(ctx, slog.LevelWarn) {
		t.Error("Warn should be enabled when level is Warn")
	}

	if !handler.Enabled(ctx, slog.LevelError) {
		t.Error("Error should be enabled when level is Warn")
	}
}

func TestWithGroup(t *testing.T) {
	buf := &bytes.Buffer{}
	handler := NewHandler(
		WithWriter(buf),
		WithoutTimestamp(),
	)

	logger := slog.New(handler).WithGroup("database").WithGroup("connection")
	logger.Info("connection established", "host", "localhost")

	output := buf.String()

	if !strings.Contains(output, "database") {
		t.Errorf("Expected grouped attribute format not found in output: %q", output)
	}
}

func TestLevelFormatting(t *testing.T) {
	buf := &bytes.Buffer{}
	handler := NewHandler(
		WithWriter(buf),
		WithoutTimestamp(),
	)

	logger := slog.New(handler)
	logger.Error("test error")

	output := buf.String()

	// Level should be formatted with consistent width
	if !strings.Contains(output, "level=") {
		t.Errorf("Expected level formatting not found in output: %q", output)
	}
}

func BenchmarkColorizeFieldValues(b *testing.B) {
	handler := NewHandler(WithColors())
	input := "time=2023-01-01T12:00:00Z level=INFO msg=hello user=john database=postgres version=1.0.0"

	b.ResetTimer()
	for range b.N {
		handler.colorizeFieldValues(input, slog.LevelInfo, "")
	}
}

func BenchmarkReefHandle(b *testing.B) {
	buf := &bytes.Buffer{}
	handler := NewHandler(
		WithWriter(buf),
		WithColors(),
		WithKeyColors(map[string]string{
			"database": "\033[95m",
			"version":  "\033[96m",
		}),
	)

	logger := slog.New(handler)
	ts := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		buf.Reset()
		logger.Info("benchmark message",
			"user", "john",
			"database", "postgres",
			"version", "1.0.0",
			"timestamp", ts)
	}
}

func BenchmarkReefHandleWithAttrs(b *testing.B) {
	buf := &bytes.Buffer{}
	handler := NewHandler(WithWriter(buf), WithColors())
	logger := slog.New(handler).With("service", "api", "env", "prod")
	ts := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		buf.Reset()
		logger.Info("benchmark message", "user", "john", "timestamp", ts)
	}
}

func BenchmarkSlogHandle(b *testing.B) {
	buf := &bytes.Buffer{}

	vanillaLogger := slog.New(slog.NewTextHandler(buf, nil))
	ts := time.Now()
	b.ReportAllocs()
	for b.Loop() {
		buf.Reset()
		vanillaLogger.Info("benchmark message",
			"user", "john",
			"database", "postgres",
			"version", "1.0.0",
			"timestamp", ts)
	}
}
