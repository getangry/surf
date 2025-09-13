# Reef Logger

Reef is a powerful, customizable slog handler for Go that enhances log output with ANSI color support, flexible formatting options, and advanced field colorization. It wraps standard `slog` handlers to provide beautiful, readable logs in both development and production environments.

## Features

- **ANSI Color Support**: Colorize log levels, field keys, and values
- **Multiple Output Formats**: Text and JSON handlers
- **Field-Specific Coloring**: Assign custom colors to specific field keys
- **Flexible Configuration**: Extensive customization options
- **Performance Optimized**: Minimal overhead compared to standard slog
- **Timestamp Control**: Custom formats or remove timestamps entirely
- **Source Code Location**: Optional file/line number tracking
- **Dynamic Line Coloring**: Color entire log lines based on attributes
- **Log Forking**: Write logs to multiple destinations simultaneously
- **Backtrace Support**: Built-in function for stack trace information

## Installation

```bash
go get github.com/getangry/surf/pkg/logger/reef
```

## Quick Start

### Basic Usage

```go
package main

import (
    "log/slog"
    "github.com/getangry/surf/pkg/logger/reef"
)

func main() {
    // Create a basic colored text handler
    handler := reef.NewHandler(
        reef.WithColors(),
    )
    
    logger := slog.New(handler)
    logger.Info("Application started", "version", "1.0.0", "env", "production")
}
```

### JSON Output

```go
// Use JSON format for production environments
handler := reef.NewHandler(
    reef.WithHandlerType(reef.JSONHandler),
    reef.WithWriter(os.Stdout),
)

logger := slog.New(handler)
logger.Info("JSON formatted log", "user", "john", "action", "login")
```

## Configuration Options

### Output Format

```go
// Text format (default)
handler := reef.NewHandler(
    reef.WithHandlerType(reef.TextHandler),
)

// JSON format
handler := reef.NewHandler(
    reef.WithHandlerType(reef.JSONHandler),
)
```

### Color Configuration

#### Enable/Disable Colors

```go
// Enable colors with default configuration
handler := reef.NewHandler(
    reef.WithColors(),
)

// Disable all colors
handler := reef.NewHandler(
    reef.WithoutColors(),
)
```

#### Custom Field Colors

```go
// Set color for specific field keys
handler := reef.NewHandler(
    reef.WithColors(),
    reef.WithKeyColor("error", "\033[91m"),     // Bright red for error fields
    reef.WithKeyColor("success", "\033[92m"),   // Bright green for success fields
)

// Or use multiple colors at once
handler := reef.NewHandler(
    reef.WithColors(),
    reef.WithKeyColors(map[string]string{
        "database": "\033[95m",  // Bright magenta
        "api":      "\033[96m",  // Bright cyan
        "cache":    "\033[93m",  // Bright yellow
    }),
)
```

#### Level-Based Coloring

```go
// Use default level colors
handler := reef.NewHandler(
    reef.WithColors(),
    reef.WithLevelColors(),
)

// Custom level colors
handler := reef.NewHandler(
    reef.WithColors(),
    reef.WithCustomLevelColors(map[slog.Level]string{
        slog.LevelDebug: "\033[90m",  // Bright black (gray)
        slog.LevelInfo:  "\033[97m",  // Bright white
        slog.LevelWarn:  "\033[93m",  // Bright yellow
        slog.LevelError: "\033[91m",  // Bright red
    }),
)

// Color entire log line based on level
handler := reef.NewHandler(
    reef.WithColors(),
    reef.WithLevelLineColoring(),
)
```

### Dynamic Line Coloring

Color entire log lines dynamically using a special attribute:

```go
handler := reef.NewHandler(
    reef.WithColors(),
    reef.WithColorAttrKey("_c"),  // Default is "_c" (reef.Color constant)
)

logger := slog.New(handler)

// Color this specific log line red
logger.Info("Critical operation", reef.Color, "red", "operation", "delete")

// Color using ANSI codes directly
logger.Info("Success message", reef.Color, "\033[92m", "status", "complete")

// Use with context attributes
contextLogger := logger.With(reef.Color, "yellow")
contextLogger.Warn("Warning context", "attempts", 3)
```

### Timestamp Configuration

```go
// Remove timestamps completely
handler := reef.NewHandler(
    reef.WithoutTimestamp(),
)

// Custom timestamp format
handler := reef.NewHandler(
    reef.WithTimestampFormat("2006-01-02 15:04:05"),
)

// Time only
handler := reef.NewHandler(
    reef.WithTimestampFormat("15:04:05.000"),
)
```

### Log Level Configuration

```go
// Set minimum log level
handler := reef.NewHandler(
    reef.WithLevel(slog.LevelWarn),  // Only WARN and ERROR logs
)

// Or use full slog options
handler := reef.NewHandler(
    reef.WithSlogOptions(&slog.HandlerOptions{
        Level: slog.LevelDebug,
        AddSource: true,
    }),
)
```

### Source Code Location

```go
// Add source file and line information
handler := reef.NewHandler(
    reef.WithSource(),
)

logger := slog.New(handler)
logger.Error("Error with source location", "err", err)
// Output includes: source=main.go:42
```

### Output Destinations

```go
// Write to a custom writer
var buf bytes.Buffer
handler := reef.NewHandler(
    reef.WithWriter(&buf),
)

// Fork output to both stdout and a file
handler := reef.NewHandler(
    reef.WithWriter(os.Stdout),
    reef.WithForkedOutfile("/var/log/app.log"),
)
```

## Advanced Examples

### Production Configuration

```go
// Production setup with JSON output and file logging
handler := reef.NewHandler(
    reef.WithHandlerType(reef.JSONHandler),
    reef.WithLevel(slog.LevelInfo),
    reef.WithTimestampFormat(time.RFC3339),
    reef.WithForkedOutfile("/var/log/myapp.log"),
)

logger := slog.New(handler)
```

### Development Configuration

```go
// Development setup with rich colors and debugging features
handler := reef.NewHandler(
    reef.WithColors(),
    reef.WithLevelLineColoring(),
    reef.WithLevel(slog.LevelDebug),
    reef.WithSource(),
    reef.WithKeyColors(map[string]string{
        "sql":      "\033[94m",  // Blue for SQL queries
        "duration": "\033[93m",  // Yellow for durations
        "error":    "\033[91m",  // Red for errors
    }),
    reef.WithTimestampFormat("15:04:05"),
)

logger := slog.New(handler)
```

### Contextual Logging with Groups

```go
handler := reef.NewHandler(reef.WithColors())
logger := slog.New(handler)

// Create grouped attributes
dbLogger := logger.WithGroup("database").With("host", "localhost")
dbLogger.Info("Connected", "port", 5432, "ssl", true)

// Nested groups
apiLogger := logger.WithGroup("api").WithGroup("v2")
apiLogger.Info("Request handled", "method", "GET", "path", "/users")
```

### Using Backtrace

```go
handler := reef.NewHandler(reef.WithColors())
logger := slog.New(handler)

// Add backtrace to error logs
func processData(data string) error {
    if err := validate(data); err != nil {
        logger.Error("Validation failed", 
            "error", err,
            reef.Backtrace(),  // Adds file:line (function) info
        )
        return err
    }
    return nil
}

// Custom backtrace offset
logger.Error("Deep error", 
    reef.Backtrace(2),  // Skip 2 additional stack frames
)
```

### Component-Based Coloring

```go
// Different colors for different system components
handler := reef.NewHandler(
    reef.WithColors(),
    reef.WithColorAttrKey("component"),
)

logger := slog.New(handler)

// Database logs in blue
dbLogger := logger.With("component", "blue")
dbLogger.Info("Query executed", "duration", "125ms")

// API logs in cyan
apiLogger := logger.With("component", "cyan")
apiLogger.Info("Request processed", "status", 200)

// Cache logs in yellow
cacheLogger := logger.With("component", "yellow")
cacheLogger.Info("Cache hit", "key", "user:123")
```

## Color Reference

### Standard Colors
- `black`, `red`, `green`, `yellow`, `blue`, `magenta`, `cyan`, `white`

### Bright Colors
- `bright_black`, `bright_red`, `bright_green`, `bright_yellow`
- `bright_blue`, `bright_magenta`, `bright_cyan`, `bright_white`

### Background Colors
- `bg_black`, `bg_red`, `bg_green`, `bg_yellow`
- `bg_blue`, `bg_magenta`, `bg_cyan`, `bg_white`

### ANSI Codes
You can also use raw ANSI escape codes:
- `\033[31m` - Red
- `\033[32m` - Green
- `\033[33m` - Yellow
- `\033[34m` - Blue
- `\033[35m` - Magenta
- `\033[36m` - Cyan
- `\033[37m` - White
- `\033[91m` - Bright Red
- `\033[92m` - Bright Green
- etc.

## Performance

Reef is designed to have minimal overhead compared to standard slog handlers. The colorization is applied efficiently during output formatting, and when colors are disabled or JSON format is used, the performance is nearly identical to vanilla slog.

```go
// Benchmark comparison (from reef_test.go)
// BenchmarkReefHandle-8       500000      2847 ns/op
// BenchmarkSlogHandle-8       500000      2654 ns/op
```

## Best Practices

1. **Use JSON in Production**: For production logs that will be processed by log aggregators, use JSON format
2. **Leverage Groups**: Use WithGroup() for structured, hierarchical logging
3. **Color Sparingly**: In development, use colors to highlight important fields rather than coloring everything
4. **Standard Field Names**: Establish conventions for field names across your application
5. **Appropriate Log Levels**: Use Debug for detailed tracing, Info for normal operations, Warn for recoverable issues, and Error for failures

## Testing

Run the test suite:

```bash
go test ./...

# With benchmarks
go test -bench=. ./...

# With coverage
go test -cover ./...
```

## License

Part of the Surf project. See the main project repository for license information.