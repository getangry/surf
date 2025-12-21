package reef

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"
)

// HandlerType defines the output format for the custom handler
type HandlerType int

const (
	TextHandler HandlerType = iota
	JSONHandler

	// Shorthand for the default color attribute key
	Color = "_c"
)

// ColorConfig contains settings for field value colorization
type ColorConfig struct {
	Enabled         bool
	DimColor        string
	BrightenColor   string
	ResetColor      string
	KeyColors       map[string]string
	LevelColors     map[slog.Level]string
	ColorEntireLine bool
	ColorAttrKey    string
}

// DefaultColorConfig provides default ANSI color codes for dimming
var DefaultColorConfig = ColorConfig{
	Enabled:       true,
	DimColor:      "\033[2m",  // ANSI dim/faint
	BrightenColor: "\033[22m", // ANSI dim/faint
	ResetColor:    "\033[0m",  // ANSI reset
	ColorAttrKey:  Color,      // Default attribute key for per-line colors
}

// DefaultLevelColors provides default ANSI color codes for log levels
var DefaultLevelColors = map[slog.Level]string{
	slog.LevelDebug: "\033[36m", // Cyan
	slog.LevelInfo:  "\033[37m", // White
	slog.LevelWarn:  "\033[33m", // Yellow
	slog.LevelError: "\033[31m", // Red
}

// ColorNameMap maps color names to ANSI codes for dynamic line coloring
var ColorNameMap = map[string]string{
	"black":   "\033[30m",
	"red":     "\033[31m",
	"green":   "\033[32m",
	"yellow":  "\033[33m",
	"blue":    "\033[34m",
	"magenta": "\033[35m",
	"cyan":    "\033[36m",
	"white":   "\033[37m",

	// Bright colors
	"bright_black":   "\033[90m",
	"bright_red":     "\033[91m",
	"bright_green":   "\033[92m",
	"bright_yellow":  "\033[93m",
	"bright_blue":    "\033[94m",
	"bright_magenta": "\033[95m",
	"bright_cyan":    "\033[96m",
	"bright_white":   "\033[97m",

	// Background colors
	"bg_black":   "\033[40m",
	"bg_red":     "\033[41m",
	"bg_green":   "\033[42m",
	"bg_yellow":  "\033[43m",
	"bg_blue":    "\033[44m",
	"bg_magenta": "\033[45m",
	"bg_cyan":    "\033[46m",
	"bg_white":   "\033[47m",
}

// Options configures the custom handler behavior
type Options struct {
	handlerType     HandlerType
	writer          io.Writer
	colorConfig     ColorConfig
	slogOptions     *slog.HandlerOptions
	timestampFormat string
	removeTimestamp bool
	addSource       bool
}

// Option defines a functional option for configuring the handler
type Option func(*Options)

// WithHandlerType sets the output format (JSON or Text)
func WithHandlerType(hType HandlerType) Option {
	return func(o *Options) {
		o.handlerType = hType
	}
}

// fileWriterCloser wraps a file with MultiWriter for proper cleanup
type fileWriterCloser struct {
	file   *os.File
	writer io.Writer
}

func (f *fileWriterCloser) Write(p []byte) (n int, err error) {
	return f.writer.Write(p)
}

func (f *fileWriterCloser) Close() error {
	if f.file != nil {
		return f.file.Close()
	}
	return nil
}

// WithForkedOutfile writes logs to both the current writer and a file.
// The returned file handle should be closed when the logger is no longer needed.
// Consider using WithForkedOutfileCloser for explicit cleanup control.
func WithForkedOutfile(path string) Option {
	return func(o *Options) {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			panic(fmt.Sprintf("failed to open log file %s: %v", path, err))
		}
		o.writer = io.MultiWriter(o.writer, f)
	}
}

// WithForkedOutfileCloser writes logs to both the current writer and a file,
// returning an io.Closer that should be called to properly close the file.
func WithForkedOutfileCloser(path string) (Option, io.Closer, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open log file %s: %w", path, err)
	}

	fc := &fileWriterCloser{file: f}

	opt := func(o *Options) {
		fc.writer = io.MultiWriter(o.writer, f)
		o.writer = fc
	}

	return opt, fc, nil
}

// WithWriter sets the output destination
func WithWriter(w io.Writer) Option {
	return func(o *Options) {
		o.writer = w
	}
}

// WithColorConfig sets the color configuration
func WithColorConfig(config ColorConfig) Option {
	return func(o *Options) {
		o.colorConfig = config
	}
}

// WithColors enables colors with default configuration
func WithColors() Option {
	return func(o *Options) {
		o.colorConfig = DefaultColorConfig
	}
}

// WithoutColors disables color output
func WithoutColors() Option {
	return func(o *Options) {
		o.colorConfig = ColorConfig{
			Enabled:         false,
			DimColor:        "",
			ResetColor:      "",
			KeyColors:       make(map[string]string),
			LevelColors:     make(map[slog.Level]string),
			ColorEntireLine: false,
			ColorAttrKey:    Color,
		}
	}
}

// WithColorAttrKey sets the attribute key used for dynamic line coloring
func WithColorAttrKey(key string) Option {
	return func(o *Options) {
		o.colorConfig.ColorAttrKey = key
	}
}

// WithKeyColor sets a custom color for a specific field key
func WithKeyColor(key, color string) Option {
	return func(o *Options) {
		if o.colorConfig.KeyColors == nil {
			o.colorConfig.KeyColors = make(map[string]string)
		}
		o.colorConfig.KeyColors[key] = color
	}
}

// WithKeyColors sets multiple custom colors for field keys
func WithKeyColors(colors map[string]string) Option {
	return func(o *Options) {
		if o.colorConfig.KeyColors == nil {
			o.colorConfig.KeyColors = make(map[string]string)
		}
		for key, color := range colors {
			o.colorConfig.KeyColors[key] = color
		}
	}
}

// WithLevelColors enables default level-based coloring
func WithLevelColors() Option {
	return func(o *Options) {
		o.colorConfig.LevelColors = DefaultLevelColors
	}
}

// WithCustomLevelColors sets custom colors for log levels
func WithCustomLevelColors(colors map[slog.Level]string) Option {
	return func(o *Options) {
		if o.colorConfig.LevelColors == nil {
			o.colorConfig.LevelColors = make(map[slog.Level]string)
		}
		for level, color := range colors {
			o.colorConfig.LevelColors[level] = color
		}
	}
}

// WithLevelLineColoring enables coloring the entire log line based on level
func WithLevelLineColoring() Option {
	return func(o *Options) {
		o.colorConfig.ColorEntireLine = true
		if o.colorConfig.LevelColors == nil {
			o.colorConfig.LevelColors = DefaultLevelColors
		}
	}
}

// WithSlogOptions sets the underlying slog handler options
func WithSlogOptions(opts *slog.HandlerOptions) Option {
	return func(o *Options) {
		o.slogOptions = opts
	}
}

// WithLevel sets the minimum log level
func WithLevel(level slog.Level) Option {
	return func(o *Options) {
		if o.slogOptions == nil {
			o.slogOptions = &slog.HandlerOptions{}
		}
		o.slogOptions.Level = level
	}
}

// WithSource enables source code location in logs
func WithSource() Option {
	return func(o *Options) {
		if o.slogOptions == nil {
			o.slogOptions = &slog.HandlerOptions{}
		}
		o.slogOptions.AddSource = true
		o.addSource = true
	}
}

// WithTimestampFormat sets a custom timestamp format (Go time layout)
func WithTimestampFormat(layout string) Option {
	return func(o *Options) {
		o.timestampFormat = layout
		o.removeTimestamp = false
	}
}

// WithoutTimestamp removes timestamps from log output
func WithoutTimestamp() Option {
	return func(o *Options) {
		o.removeTimestamp = true
		o.timestampFormat = ""
	}
}

// Handler wraps standard slog handlers with enhanced formatting options
type Handler struct {
	handler         slog.Handler
	config          ColorConfig
	writer          io.Writer
	hType           HandlerType
	timestampFormat string
	removeTimestamp bool
	addSource       bool
	attrs           []slog.Attr
	groups          []string
}

// NewHandler creates a new custom handler with the specified options
func NewHandler(opts ...Option) *Handler {
	// Sets default options
	options := &Options{
		handlerType: TextHandler,
		writer:      os.Stdout,
		colorConfig: DefaultColorConfig,
		slogOptions: &slog.HandlerOptions{},
		addSource:   false,
	}

	// Applies provided options
	for _, opt := range opts {
		opt(options)
	}

	// Creates the base handler with custom replace function if needed
	baseOptions := options.slogOptions
	if baseOptions == nil {
		baseOptions = &slog.HandlerOptions{}
	}

	// Customizes the replace function for timestamp handling
	if options.removeTimestamp || options.timestampFormat != "" {
		originalReplace := baseOptions.ReplaceAttr
		baseOptions.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
			// Calls original replace function first if it exists
			if originalReplace != nil {
				a = originalReplace(groups, a)
			}

			// Handles timestamp customization
			if a.Key == slog.TimeKey {
				if options.removeTimestamp {
					return slog.Attr{} // Returns empty attribute to remove it
				}
				if options.timestampFormat != "" {
					if t, ok := a.Value.Any().(time.Time); ok {
						return slog.String(slog.TimeKey, t.Format(options.timestampFormat))
					}
				}
			}
			return a
		}
	}

	var baseHandler slog.Handler
	switch options.handlerType {
	case JSONHandler:
		baseHandler = slog.NewJSONHandler(options.writer, baseOptions)
	default:
		baseHandler = slog.NewTextHandler(options.writer, baseOptions)
	}

	return &Handler{
		handler:         baseHandler,
		config:          options.colorConfig,
		writer:          options.writer,
		hType:           options.handlerType,
		timestampFormat: options.timestampFormat,
		removeTimestamp: options.removeTimestamp,
		addSource:       options.addSource,
		attrs:           make([]slog.Attr, 0),
		groups:          make([]string, 0),
	}
}

// extractLineColor extracts and removes color attribute from log record and handler attributes
func (h *Handler) extractLineColor(record *slog.Record) (string, []slog.Attr) {
	if h.config.ColorAttrKey == "" {
		return "", h.attrs
	}

	var lineColor string
	var newRecordAttrs []slog.Attr
	var newHandlerAttrs []slog.Attr

	// Checks handler's persistent attributes first (from .With() calls)
	for _, attr := range h.attrs {
		if attr.Key == h.config.ColorAttrKey {
			colorValue := attr.Value.String()
			// Removes quotes if present
			if len(colorValue) >= 2 && colorValue[0] == '"' && colorValue[len(colorValue)-1] == '"' {
				colorValue = colorValue[1 : len(colorValue)-1]
			}
			lineColor = h.parseColorValue(colorValue)
		} else {
			newHandlerAttrs = append(newHandlerAttrs, attr)
		}
	}

	// Checks record's immediate attributes (from log call itself)
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == h.config.ColorAttrKey {
			colorValue := attr.Value.String()
			// Removes quotes if present
			if len(colorValue) >= 2 && colorValue[0] == '"' && colorValue[len(colorValue)-1] == '"' {
				colorValue = colorValue[1 : len(colorValue)-1]
			}
			// Record attributes take precedence over handler attributes
			lineColor = h.parseColorValue(colorValue)
		} else {
			newRecordAttrs = append(newRecordAttrs, attr)
		}
		return true
	})

	// Creates new record without the color attribute if it was found in record
	if len(newRecordAttrs) != 0 || lineColor != "" {
		newRecord := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
		newRecord.AddAttrs(newRecordAttrs...)
		*record = newRecord
	}

	return lineColor, newHandlerAttrs
}

// parseColorValue converts color name or ANSI code to ANSI escape sequence
func (h *Handler) parseColorValue(colorValue string) string {
	// Checks if it's already an ANSI escape sequence
	if strings.HasPrefix(colorValue, "\033[") {
		return colorValue
	}

	// Looks up color name in the map
	if ansiCode, exists := ColorNameMap[colorValue]; exists {
		return ansiCode
	}

	// Returns empty string if color is not recognized
	return ""
}

// Handle processes log records and applies colorization to field values
func (h *Handler) Handle(ctx context.Context, record slog.Record) error {
	var tempHandler slog.Handler

	// if !h.config.Enabled || h.hType == JSONHandler {
	if !h.config.Enabled {
		// Uses base handler without modification for JSON or when colors disabled
		return h.handler.Handle(ctx, record)
	}

	// Extracts per-line color attribute if present and gets filtered handler attributes
	lineColor, filteredHandlerAttrs := h.extractLineColor(&record)

	// Creates a custom buffer to capture and modify the output
	var buf strings.Builder
	if h.hType == JSONHandler {
		tempHandler = slog.NewJSONHandler(&buf, &slog.HandlerOptions{
			Level:       h.getLevel(),
			AddSource:   h.getAddSource(),
			ReplaceAttr: h.getReplaceAttr(),
		})
	} else {
		tempHandler = slog.NewTextHandler(&buf, &slog.HandlerOptions{
			Level:       h.getLevel(),
			AddSource:   h.getAddSource(),
			ReplaceAttr: h.getReplaceAttr(),
		})
	}

	// Applies groups to temp handler
	for _, group := range h.groups {
		tempHandler = tempHandler.WithGroup(group).(slog.Handler)
	}

	// Applies filtered attributes to temp handler (without color attribute)
	if len(filteredHandlerAttrs) > 0 {
		tempHandler = tempHandler.WithAttrs(filteredHandlerAttrs).(slog.Handler)
	}

	err := tempHandler.Handle(ctx, record)
	if err != nil {
		return err
	}

	// Applies color formatting to the captured output
	colorized := h.colorizeFieldValues(buf.String(), record.Level, lineColor)
	_, writeErr := h.writer.Write([]byte(colorized))
	return writeErr
}

// getLevel extracts the level from the wrapped handler options
func (h *Handler) getLevel() slog.Level {
	// Attempts to get level from the handler, defaults to Info
	if leveler, ok := h.handler.(interface {
		Enabled(context.Context, slog.Level) bool
	}); ok {
		for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError} {
			if leveler.Enabled(context.Background(), level) {
				return level
			}
		}
	}
	return slog.LevelInfo
}

// getAddSource determines if source information should be included
func (h *Handler) getAddSource() bool {
	return h.addSource
}

// getReplaceAttr gets the replace attribute function for timestamp handling
func (h *Handler) getReplaceAttr() func(groups []string, a slog.Attr) slog.Attr {
	if !h.removeTimestamp && h.timestampFormat == "" {
		return nil
	}

	return func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			if h.removeTimestamp {
				return slog.Attr{}
			}
			if h.timestampFormat != "" {
				if t, ok := a.Value.Any().(time.Time); ok {
					return slog.String(slog.TimeKey, t.Format(h.timestampFormat))
				}
			}
		}
		return a
	}
}

// colorizeFieldValues applies dim coloring to field values in text output
func (h *Handler) colorizeFieldValues(text string, level slog.Level, lineColor string) string {
	if !h.config.Enabled {
		return text
	}

	lines := strings.Split(text, "\n")
	var result strings.Builder

	for _, line := range lines {
		if line == "" {
			continue // Skips empty lines
		}

		var processedLine string
		if strings.Contains(line, "=") {
			processedLine = h.colorizeLineFields(line, level, lineColor)
		} else {
			processedLine = line
			// Applies line coloring to non-field lines if enabled
			effectiveColor := h.getEffectiveLineColor(level, lineColor)
			if effectiveColor != "" {
				processedLine = effectiveColor + processedLine + h.config.ResetColor
			}
		}

		result.WriteString(processedLine)

		// Preserves original line endings
		if line != lines[len(lines)-1] {
			result.WriteString("\n")
		}
	}

	return result.String()
}

// getEffectiveLineColor determines the color to use for the line based on priority
func (h *Handler) getEffectiveLineColor(level slog.Level, lineColor string) string {
	// Per-line color attribute has highest priority
	if lineColor != "" {
		return lineColor
	}

	// Level-based line coloring is next priority
	if h.config.ColorEntireLine && len(h.config.LevelColors) > 0 {
		if levelColor, exists := h.config.LevelColors[level]; exists {
			return levelColor
		}
	}

	return ""
}

// colorizeLineFields processes a single line and colors field values with level-aware coloring
func (h *Handler) colorizeLineFields(line string, level slog.Level, lineColor string) string {
	var result strings.Builder
	i := 0

	// Determines effective line color (per-line attribute overrides level color)
	effectiveLineColor := h.getEffectiveLineColor(level, lineColor)

	// Determines base colors for this level/line
	var baseKeyColor, baseValueColor string
	if effectiveLineColor != "" {
		baseKeyColor = effectiveLineColor + h.config.DimColor
		baseValueColor = effectiveLineColor + h.config.BrightenColor
	} else {
		baseKeyColor = h.config.DimColor
		baseValueColor = h.config.BrightenColor
	}

	for i < len(line) {
		// Finds the next key=value pattern
		eqIndex := strings.Index(line[i:], "=")
		if eqIndex == -1 {
			// No more key=value pairs, append the rest with line coloring if enabled
			remaining := line[i:]
			if effectiveLineColor != "" {
				remaining = effectiveLineColor + remaining + h.config.ResetColor
			}
			result.WriteString(remaining)
			break
		}

		// Adjusts index to absolute position
		eqIndex += i

		// Finds the start of the key (works backwards from =)
		keyStart := eqIndex
		for keyStart > i && line[keyStart-1] != ' ' {
			keyStart--
		}

		// Writes everything before this key=value pair with line coloring if enabled
		prefix := line[i:keyStart]
		if prefix != "" && effectiveLineColor != "" {
			prefix = effectiveLineColor + prefix + h.config.ResetColor
		}
		result.WriteString(prefix)

		// Extracts the key
		key := line[keyStart:eqIndex]

		// Determines color to use for this key (custom colors override line/level colors)
		var keyColor, valueColor string
		if customColor, exists := h.config.KeyColors[key]; exists {
			keyColor = customColor + h.config.DimColor     // Dimmed version of custom color
			valueColor = h.config.ResetColor + customColor // Full custom color for value
		} else {
			keyColor = baseKeyColor
			valueColor = baseValueColor
		}

		// Applies key coloring
		result.WriteString(keyColor)
		result.WriteString(key)
		result.WriteString("=")

		// Finds the value after the =
		valueStart := eqIndex + 1
		valueEnd := h.findValueEnd(line, valueStart)

		// Extracts and colors the value
		value := line[valueStart:valueEnd]
		if key == slog.LevelKey {
			value = fmt.Sprintf("%-5s", value)
		}

		result.WriteString(valueColor)
		result.WriteString(value)
		result.WriteString(h.config.ResetColor)

		// Moves to the next position
		i = valueEnd
	}

	return result.String()
}

// findValueEnd determines where a field value ends, handling quoted strings
func (h *Handler) findValueEnd(line string, start int) int {
	if start >= len(line) {
		return start
	}

	// Handles quoted values
	if line[start] == '"' {
		// Finds the closing quote, handling escaped quotes
		i := start + 1
		for i < len(line) {
			if line[i] == '"' {
				// Checks if this quote is escaped
				escaped := false
				backslashes := 0
				for j := i - 1; j >= 0 && line[j] == '\\'; j-- {
					backslashes++
				}
				escaped = backslashes%2 == 1

				if !escaped {
					return i + 1 // Includes the closing quote
				}
			}
			i++
		}
		return len(line) // Unclosed quote, goes to end
	}

	// Handles unquoted values (stops at space or end of line)
	i := start
	for i < len(line) && line[i] != ' ' {
		i++
	}
	return i
}

// Enabled reports whether the handler handles records at the given level
func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.handler.Enabled(ctx, level)
}

// WithAttrs returns a new handler with the given attributes added
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	// Combines existing attributes with new ones
	newAttrs := make([]slog.Attr, len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	copy(newAttrs[len(h.attrs):], attrs)

	// Copies existing groups
	newGroups := make([]string, len(h.groups))
	copy(newGroups, h.groups)

	// Creates handler attributes without color attribute for the underlying handler
	var handlerAttrs []slog.Attr
	for _, attr := range newAttrs {
		if attr.Key != h.config.ColorAttrKey {
			handlerAttrs = append(handlerAttrs, attr)
		}
	}

	return &Handler{
		handler:         h.handler.WithAttrs(handlerAttrs),
		config:          h.config,
		writer:          h.writer,
		hType:           h.hType,
		timestampFormat: h.timestampFormat,
		removeTimestamp: h.removeTimestamp,
		addSource:       h.addSource,
		attrs:           newAttrs, // Keeps all attributes including color for extraction
		groups:          newGroups,
	}
}

// WithGroup returns a new handler with the given group name
func (h *Handler) WithGroup(name string) slog.Handler {
	// Adds the new group to the existing groups
	newGroups := make([]string, len(h.groups)+1)
	copy(newGroups, h.groups)
	newGroups[len(h.groups)] = name

	// Copies existing attributes
	newAttrs := make([]slog.Attr, len(h.attrs))
	copy(newAttrs, h.attrs)

	// Creates handler attributes without color attribute for the underlying handler
	var handlerAttrs []slog.Attr
	for _, attr := range h.attrs {
		if attr.Key != h.config.ColorAttrKey {
			handlerAttrs = append(handlerAttrs, attr)
		}
	}

	return &Handler{
		handler:         h.handler.WithGroup(name).WithAttrs(handlerAttrs),
		config:          h.config,
		writer:          h.writer,
		hType:           h.hType,
		timestampFormat: h.timestampFormat,
		removeTimestamp: h.removeTimestamp,
		attrs:           newAttrs, // Keeps all attributes including color for extraction
		groups:          newGroups,
	}
}

func Backtrace(offset ...int) slog.Attr {
	skip := 1
	if len(offset) > 0 {
		skip += offset[0]
	}

	pc, file, line, ok := runtime.Caller(skip) // Skip two levels to get the caller of Backtrace
	if !ok {
		return slog.String("backtrace", "n/a")
	}

	fn := runtime.FuncForPC(pc)
	funcName := "n/a"
	if fn != nil {
		funcName = fn.Name()
	}

	// Formats: file:line (function)
	formatted := fmt.Sprintf("%s:%d (%s)", file, line, funcName)
	return slog.String("backtrace", formatted)
}
