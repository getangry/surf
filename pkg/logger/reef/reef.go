// Package reef is a slog.Handler that renders colored, structured log
// records to a terminal. Useful in development; switch to a JSON or text
// handler in production.
package reef

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
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

	// Derived state, precomputed once per handler in finalize() so the
	// per-record Handle path stays allocation-light.
	handlerAttrs     []slog.Attr          // attrs minus the color attribute
	handlerLineColor string               // line color carried by handler attrs
	keyColorPairs    map[string][2]string // per-key {keyColor, valueColor}
	rpool            *sync.Pool           // pool of *renderer
}

// renderer holds a reusable slog handler bound to a private buffer, plus a
// second buffer for the colorized output. Renderers are pooled so the
// expensive slog handler construction and buffer growth happen once and are
// amortized across log records. A renderer is owned exclusively by a single
// Handle call while checked out of the pool, so it needs no locking.
type renderer struct {
	handler slog.Handler
	buf     bytes.Buffer // raw slog output
	out     bytes.Buffer // colorized output
}

// newRenderer builds a renderer whose slog handler already has this handler's
// groups and attributes applied, bound to the renderer's private buffer.
func (h *Handler) newRenderer() *renderer {
	r := &renderer{}
	opts := &slog.HandlerOptions{
		Level:       h.getLevel(),
		AddSource:   h.addSource,
		ReplaceAttr: h.getReplaceAttr(),
	}
	var th slog.Handler
	if h.hType == JSONHandler {
		th = slog.NewJSONHandler(&r.buf, opts)
	} else {
		th = slog.NewTextHandler(&r.buf, opts)
	}
	for _, group := range h.groups {
		th = th.WithGroup(group)
	}
	if len(h.handlerAttrs) > 0 {
		th = th.WithAttrs(h.handlerAttrs)
	}
	r.handler = th
	return r
}

// finalize precomputes the derived state that Handle relies on. It must be
// called after the core fields (handler, config, attrs, groups, ...) are set
// and before the handler is used, and the handler must be treated as immutable
// afterwards.
func (h *Handler) finalize() {
	// Split persistent attrs into those forwarded to slog and the line color.
	h.handlerLineColor = ""
	h.handlerAttrs = nil
	if len(h.attrs) > 0 {
		h.handlerAttrs = make([]slog.Attr, 0, len(h.attrs))
		for _, attr := range h.attrs {
			if h.config.ColorAttrKey != "" && attr.Key == h.config.ColorAttrKey {
				h.handlerLineColor = h.parseColorValue(unquote(attr.Value.String()))
			} else {
				h.handlerAttrs = append(h.handlerAttrs, attr)
			}
		}
	}

	// Custom key colors are independent of level and per-line color, so the
	// concatenated escape sequences can be built once here.
	if len(h.config.KeyColors) > 0 {
		h.keyColorPairs = make(map[string][2]string, len(h.config.KeyColors))
		for key, color := range h.config.KeyColors {
			h.keyColorPairs[key] = [2]string{
				color + h.config.DimColor,   // dimmed custom color for the key
				h.config.ResetColor + color, // full custom color for the value
			}
		}
	}

	h.rpool = &sync.Pool{New: func() any { return h.newRenderer() }}
}

// unquote strips a single pair of surrounding double quotes if present.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
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

	h := &Handler{
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
	h.finalize()
	return h
}

// extractRecordColor resolves the effective per-line color for a record and,
// only when the record actually carries a color attribute, rebuilds the record
// without it so the attribute is not rendered. The handler's persistent color
// (from .With()) is precomputed in finalize, so the common path scans the
// record once and never reallocates it.
func (h *Handler) extractRecordColor(record *slog.Record) string {
	lineColor := h.handlerLineColor
	if h.config.ColorAttrKey == "" {
		return lineColor
	}

	// Cheap detection pass: most records have no color attribute, in which
	// case the record is left untouched.
	hasColor := false
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == h.config.ColorAttrKey {
			hasColor = true
			return false
		}
		return true
	})
	if !hasColor {
		return lineColor
	}

	// Rebuild the record without the color attribute. Record attributes take
	// precedence over the handler-level color.
	kept := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		if attr.Key == h.config.ColorAttrKey {
			lineColor = h.parseColorValue(unquote(attr.Value.String()))
		} else {
			kept = append(kept, attr)
		}
		return true
	})
	newRecord := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	newRecord.AddAttrs(kept...)
	*record = newRecord

	return lineColor
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
	if !h.config.Enabled {
		// Uses base handler without modification when colors are disabled
		return h.handler.Handle(ctx, record)
	}

	// Resolves the per-line color, stripping the color attribute from the
	// record only when one is actually present.
	lineColor := h.extractRecordColor(&record)

	// Checks out a pooled renderer whose slog handler already carries this
	// handler's groups and attributes, so nothing is reconstructed per record.
	r := h.rpool.Get().(*renderer)
	r.buf.Reset()
	r.out.Reset()

	if err := r.handler.Handle(ctx, record); err != nil {
		h.rpool.Put(r)
		return err
	}

	// Colorizes the raw slog output into the renderer's output buffer and
	// writes it in a single call.
	h.colorizeInto(&r.out, r.buf.Bytes(), record.Level, lineColor)
	_, writeErr := h.writer.Write(r.out.Bytes())
	h.rpool.Put(r)
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

// colorizeFieldValues applies dim coloring to field values in text output.
// It is a string-based convenience wrapper around colorizeInto.
func (h *Handler) colorizeFieldValues(text string, level slog.Level, lineColor string) string {
	if !h.config.Enabled {
		return text
	}
	var out bytes.Buffer
	h.colorizeInto(&out, []byte(text), level, lineColor)
	return out.String()
}

// colorizeInto colorizes raw slog output and appends the result to out. It
// iterates lines in place without splitting into a slice and writes directly
// into the output buffer to avoid per-line allocations.
func (h *Handler) colorizeInto(out *bytes.Buffer, text []byte, level slog.Level, lineColor string) {
	n := len(text)
	for i := 0; i < n; {
		// Finds the end of the current line.
		j := i
		for j < n && text[j] != '\n' {
			j++
		}
		line := text[i:j]

		if len(line) > 0 {
			if bytes.IndexByte(line, '=') >= 0 {
				h.colorizeLineFieldsInto(out, line, level, lineColor)
			} else if effectiveColor := h.getEffectiveLineColor(level, lineColor); effectiveColor != "" {
				out.WriteString(effectiveColor)
				out.Write(line)
				out.WriteString(h.config.ResetColor)
			} else {
				out.Write(line)
			}

			// Preserves the line terminator when this segment had one.
			if j < n {
				out.WriteByte('\n')
			}
		}

		i = j + 1
	}
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

// colorizeLineFieldsInto processes a single line and writes its colorized form
// into out, applying level-aware coloring. It operates on a byte slice and
// uses precomputed per-key colors to avoid per-field allocations.
func (h *Handler) colorizeLineFieldsInto(out *bytes.Buffer, line []byte, level slog.Level, lineColor string) {
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

	i := 0
	for i < len(line) {
		// Finds the next key=value pattern
		eqIndex := bytes.IndexByte(line[i:], '=')
		if eqIndex == -1 {
			// No more key=value pairs, append the rest with line coloring if enabled
			remaining := line[i:]
			if effectiveLineColor != "" {
				out.WriteString(effectiveLineColor)
				out.Write(remaining)
				out.WriteString(h.config.ResetColor)
			} else {
				out.Write(remaining)
			}
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
		if len(prefix) > 0 {
			if effectiveLineColor != "" {
				out.WriteString(effectiveLineColor)
				out.Write(prefix)
				out.WriteString(h.config.ResetColor)
			} else {
				out.Write(prefix)
			}
		}

		// Extracts the key
		key := line[keyStart:eqIndex]

		// Determines color to use for this key (custom colors override line/level colors).
		// The string(key) map lookups are special-cased by the compiler and do not allocate.
		keyColor, valueColor := baseKeyColor, baseValueColor
		if pair, exists := h.keyColorPairs[string(key)]; exists {
			keyColor, valueColor = pair[0], pair[1]
		}

		// Applies key coloring
		out.WriteString(keyColor)
		out.Write(key)
		out.WriteByte('=')

		// Finds the value after the =
		valueStart := eqIndex + 1
		valueEnd := findValueEnd(line, valueStart)
		value := line[valueStart:valueEnd]

		// Applies value coloring, padding the level value to a fixed width.
		out.WriteString(valueColor)
		out.Write(value)
		if string(key) == slog.LevelKey {
			for k := len(value); k < 5; k++ {
				out.WriteByte(' ')
			}
		}
		out.WriteString(h.config.ResetColor)

		// Moves to the next position
		i = valueEnd
	}
}

// findValueEnd determines where a field value ends, handling quoted strings
func findValueEnd(line []byte, start int) int {
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

	nh := &Handler{
		config:          h.config,
		writer:          h.writer,
		hType:           h.hType,
		timestampFormat: h.timestampFormat,
		removeTimestamp: h.removeTimestamp,
		addSource:       h.addSource,
		attrs:           newAttrs, // Keeps all attributes including color for extraction
		groups:          newGroups,
	}
	// finalize derives handlerAttrs (attrs without the color attribute), which
	// is exactly what the underlying handler needs.
	nh.finalize()
	nh.handler = h.handler.WithAttrs(nh.handlerAttrs)
	return nh
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

	nh := &Handler{
		config:          h.config,
		writer:          h.writer,
		hType:           h.hType,
		timestampFormat: h.timestampFormat,
		removeTimestamp: h.removeTimestamp,
		addSource:       h.addSource,
		attrs:           newAttrs, // Keeps all attributes including color for extraction
		groups:          newGroups,
	}
	nh.finalize()
	nh.handler = h.handler.WithGroup(name).WithAttrs(nh.handlerAttrs)
	return nh
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
