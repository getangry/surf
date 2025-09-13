package main

import (
	"log/slog"
	"net/http"

	"github.com/getangry/surf"
	"github.com/getangry/surf/pkg/logger/reef"
)

func main() {
	logger := slog.New(reef.NewHandler(
		reef.WithHandlerType(reef.TextHandler),
		// reef.WithSource(),
		reef.WithColors(),
		reef.WithColorAttrKey("_c"),
		reef.WithLevelLineColoring(),
		// reef.WithForkedOutfile("./surf.log"),
		reef.WithTimestampFormat("Monday, Jan-02-2006, 03:04:05 PM"),
		reef.WithLevel(slog.LevelDebug),
		// reef.WithCustomLevelColors(map[slog.Level]string{
		// 	slog.LevelDebug: "\033[38;5;109m", // Cyan
		// 	slog.LevelInfo:  "\033[38;5;102m", // White
		// 	slog.LevelWarn:  "\033[38;5;216m", // Yellow
		// 	slog.LevelError: "\033[38;5;203m", // Red
		// }),
		reef.WithKeyColors(map[string]string{
			"database.driver": "\033[95m", // Bright magenta for components
			"version":         "\033[96m", // Bright cyan for versions
		}),
	))

	slog.SetDefault(logger)
	logger.Info("Starting Surf application", "version", "1.0.0")

	// Creates logger with groups and more attributes
	dbLogger := logger.WithGroup("database").With("driver", "postgres")
	dbLogger.Warn("Connection pool exhausted", "active_connections", 50)

	appGroupLogger := logger.With("component", "app")
	appGroupLogger.Info("Creating new Surf application instance with an INFO message")
	appGroupLogger.Error("Creating new Surf application instance with an ERROR message")
	appGroupLogger.Warn("Creating new Surf application instance with a WARN message")
	appGroupLogger.Debug("Creating new Surf application instance with a DEBUG message")

	surfApp := surf.NewApp(surf.WithLogger(logger.With(reef.Color, "white").With(reef.Backtrace())))
	defer surfApp.Cleanup()

	surfApp.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello, Surf!"))
	})

	if err := surfApp.Serve(); err != nil {
		appGroupLogger.Error("Failed to start Surf application", "error", err)
	} else {
		appGroupLogger.Info("Surf application started successfully")
	}
}
