package surf

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
)

// MCPRequest is embedded in a typed handler's request struct to declare
// tool-level metadata. The embedded field's `name` and `desc` struct tags name
// and describe the tool; each request field's `desc` and `required` tags
// describe the tool's arguments.
//
//	type CreateUser struct {
//	    surf.MCPRequest `name:"create_user" desc:"Create a new user account"`
//	    Name  string `json:"name"  desc:"The user's full name" required:"true"`
//	    Email string `json:"email" desc:"Email address"`
//	}
//
// Embedding the marker alone does not expose a tool — registration is explicit
// via MCPHandle.
type MCPRequest struct{}

var mcpRequestType = reflect.TypeOf(MCPRequest{})

// MCPToolInfo is the public identity of a registered tool, passed to
// MCPOptions.ExposeWhen so an authorization policy can decide visibility.
type MCPToolInfo struct {
	Name        string
	Description string
	Route       RouteInfo
}

// MCPOptions configures an MCP endpoint mounted with App.MCP.
type MCPOptions struct {
	// ServerName and ServerVersion are reported to clients during initialize.
	ServerName    string
	ServerVersion string

	// ExposeWhen gates each tool per request. It runs for both tools/list
	// (hidden tools are omitted) and tools/call (a hidden tool is rejected as
	// if it did not exist). A nil policy exposes every registered tool.
	ExposeWhen func(c *Context, tool MCPToolInfo) bool
}

// mcpTool is the internal record for one registered tool.
type mcpTool struct {
	name        string
	description string
	route       RouteInfo
	inputSchema *Schema
}

func (t mcpTool) info() MCPToolInfo {
	return MCPToolInfo{Name: t.name, Description: t.description, Route: t.route}
}

// MCPHandle registers a typed route exactly like HandleJSON and, additionally,
// exposes it as an MCP tool. It is the only way a route becomes a tool —
// exposure is always deliberate.
//
// Tool name and description come from the `name`/`desc` tags on the embedded
// surf.MCPRequest marker in Req (the name falls back to a slug of method+path).
// The tool's input schema is derived by reflection from Req plus the route's
// path parameters.
func MCPHandle[Req any, Resp any](
	app *App,
	method, pattern string,
	fn func(c *Context, req Req) (Resp, error),
	middleware ...CtxMiddleware,
) {
	HandleJSON(app, method, pattern, fn, middleware...)

	ri := app.router.routeInfo[len(app.router.routeInfo)-1]
	name, desc := mcpToolMeta(ri.ReqType)
	if name == "" {
		name = operationID(method, pattern)
	}
	app.mcpTools = append(app.mcpTools, mcpTool{
		name:        name,
		description: desc,
		route:       ri,
		inputSchema: mcpInputSchema(ri),
	})
}

// mcpToolMeta reads the name/desc tags off the embedded MCPRequest marker.
func mcpToolMeta(reqType reflect.Type) (name, desc string) {
	if reqType == nil {
		return "", ""
	}
	t := reqType
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return "", ""
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous && f.Type == mcpRequestType {
			return f.Tag.Get("name"), f.Tag.Get("desc")
		}
	}
	return "", ""
}

// mcpInputSchema builds the tool's JSON Schema: an object whose properties are
// the path parameters (as strings) merged with the request body's fields, so a
// caller supplies everything in one flat arguments object.
func mcpInputSchema(ri RouteInfo) *Schema {
	s := &Schema{Type: "object", Properties: map[string]*Schema{}}

	for _, p := range ri.Params {
		s.Properties[p] = &Schema{Type: "string"}
		s.Required = append(s.Required, p)
	}

	if ri.ReqType != nil {
		body := SchemaFor(ri.ReqType)
		obj := body
		rootName := ""
		if body.Ref != "" && body.Defs != nil {
			rootName = strings.TrimPrefix(body.Ref, "#/$defs/")
			obj = body.Defs[rootName]
		}
		if obj != nil {
			for k, v := range obj.Properties {
				s.Properties[k] = v
			}
			s.Required = append(s.Required, obj.Required...)
		}
		// Carry nested definitions (all but the inlined root type).
		if body.Defs != nil {
			for k, v := range body.Defs {
				if k == rootName {
					continue
				}
				if s.Defs == nil {
					s.Defs = map[string]*Schema{}
				}
				s.Defs[k] = v
			}
		}
	}
	return s
}

// MCP mounts a Model Context Protocol endpoint at pattern. It speaks JSON-RPC
// 2.0 over HTTP POST (the Streamable HTTP transport, request/response only —
// no server-initiated SSE), handling initialize, tools/list, and tools/call.
//
// tools/call executes the real handler in process by reconstructing the
// equivalent HTTP request and running it through the full router — binding,
// validation, middleware, and error rendering behave identically to a live
// request.
func (app *App) MCP(pattern string, opts MCPOptions) {
	if opts.ServerName == "" {
		opts.ServerName = "surf"
	}
	if opts.ServerVersion == "" {
		opts.ServerVersion = "0.0.0"
	}
	app.Handle(http.MethodPost, pattern, func(c *Context) error {
		return app.serveMCP(c, opts)
	})
}

// --- JSON-RPC 2.0 envelopes ---

type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// JSON-RPC standard error codes used here.
const (
	rpcParseError     = -32700
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
	rpcInternalError  = -32603
)

func (app *App) serveMCP(c *Context, opts MCPOptions) error {
	var req jsonrpcRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		return c.JSON(http.StatusOK, rpcErr(nil, rpcParseError, "parse error"))
	}
	if req.JSONRPC != "2.0" {
		return c.JSON(http.StatusOK, rpcErr(req.ID, rpcInvalidRequest, "jsonrpc must be \"2.0\""))
	}

	// Notifications (no id) get an acknowledgement with no body.
	if len(req.ID) == 0 || string(req.ID) == "null" {
		return c.NoContent(http.StatusAccepted)
	}

	switch req.Method {
	case "initialize":
		return c.JSON(http.StatusOK, app.mcpInitialize(req, opts))
	case "tools/list":
		return c.JSON(http.StatusOK, app.mcpToolsList(c, req, opts))
	case "tools/call":
		return c.JSON(http.StatusOK, app.mcpToolsCall(c, req, opts))
	default:
		return c.JSON(http.StatusOK, rpcErr(req.ID, rpcMethodNotFound, "unknown method: "+req.Method))
	}
}

func (app *App) mcpInitialize(req jsonrpcRequest, opts MCPOptions) jsonrpcResponse {
	// Echo the client's requested protocol version when present.
	protocol := "2025-06-18"
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(req.Params) > 0 && json.Unmarshal(req.Params, &p) == nil && p.ProtocolVersion != "" {
		protocol = p.ProtocolVersion
	}
	return jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"protocolVersion": protocol,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{
				"name":    opts.ServerName,
				"version": opts.ServerVersion,
			},
		},
	}
}

func (app *App) mcpToolsList(c *Context, req jsonrpcRequest, opts MCPOptions) jsonrpcResponse {
	tools := make([]map[string]any, 0, len(app.mcpTools))
	for _, t := range app.mcpTools {
		if opts.ExposeWhen != nil && !opts.ExposeWhen(c, t.info()) {
			continue
		}
		tools = append(tools, map[string]any{
			"name":        t.name,
			"description": t.description,
			"inputSchema": t.inputSchema,
		})
	}
	return jsonrpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": tools}}
}

func (app *App) mcpToolsCall(c *Context, req jsonrpcRequest, opts MCPOptions) jsonrpcResponse {
	var params struct {
		Name      string                     `json:"name"`
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return rpcErr(req.ID, rpcInvalidParams, "invalid params")
	}

	var tool *mcpTool
	for i := range app.mcpTools {
		if app.mcpTools[i].name == params.Name {
			tool = &app.mcpTools[i]
			break
		}
	}
	// A hidden tool is indistinguishable from a missing one.
	if tool == nil || (opts.ExposeWhen != nil && !opts.ExposeWhen(c, tool.info())) {
		return rpcErr(req.ID, rpcInvalidParams, "unknown tool: "+params.Name)
	}

	status, body := app.mcpInvoke(c, *tool, params.Arguments)
	return jsonrpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"content": []map[string]any{{"type": "text", "text": string(body)}},
			"isError": status >= 400,
		},
	}
}

// mcpInvoke reconstructs the HTTP request a tool call maps to and dispatches it
// through the full router, returning the handler's status and response body.
// Path-parameter arguments are substituted into the path; the remaining
// arguments form the JSON body. The incoming request's headers are copied so
// downstream auth middleware sees the same identity.
func (app *App) mcpInvoke(c *Context, tool mcpTool, args map[string]json.RawMessage) (int, []byte) {
	path := tool.route.Pattern
	// filled tracks each path parameter and whether the call supplied it.
	filled := make(map[string]bool, len(tool.route.Params))
	for _, p := range tool.route.Params {
		filled[p] = false
	}

	bodyFields := make(map[string]json.RawMessage, len(args))
	for k, v := range args {
		if _, isParam := filled[k]; isParam {
			var sv string
			if json.Unmarshal(v, &sv) != nil {
				sv = strings.Trim(string(v), `"`)
			}
			// A path parameter fills exactly one path segment. The router
			// matches on the decoded r.URL.Path, so a "/" in the value — even
			// percent-encoded, which the URL layer decodes back — would split
			// into extra segments and could route the in-process request to a
			// different endpoint than the tool declares. Reject it outright.
			if strings.ContainsRune(sv, '/') {
				return http.StatusBadRequest, []byte(`{"error":"path parameter must not contain '/'"}`)
			}
			// PathEscape still guards other path-significant characters
			// (?, #, space) that would otherwise alter the request target.
			path = strings.Replace(path, ":"+k, url.PathEscape(sv), 1)
			filled[k] = true
			continue
		}
		bodyFields[k] = v
	}

	// Every path parameter must be supplied. An unfilled ":param" stays in the
	// path literally, which still matches the same route — the handler would
	// then run against a nonsense parameter value instead of failing cleanly.
	for _, p := range tool.route.Params {
		if !filled[p] {
			msg, _ := json.Marshal(map[string]string{"error": "missing required path parameter: " + p})
			return http.StatusBadRequest, msg
		}
	}

	bodyBytes, _ := json.Marshal(bodyFields)
	sub, err := http.NewRequestWithContext(c.Request.Context(), tool.route.Method, path, bytes.NewReader(bodyBytes))
	if err != nil {
		return http.StatusInternalServerError, []byte(`{"error":"could not build request"}`)
	}
	for k, vals := range c.Request.Header {
		if strings.EqualFold(k, "Content-Length") {
			continue
		}
		sub.Header[k] = vals
	}
	sub.Header.Set("Content-Type", "application/json")
	// Carry the caller's identity onto the sub-request so IP-keyed middleware
	// (rate limiting, logging, trusted-proxy resolution) sees the real client
	// rather than an empty address — a tool call must not be a way around it.
	sub.RemoteAddr = c.Request.RemoteAddr
	sub.Host = c.Request.Host

	rec := &mcpRecorder{}
	app.ServeHTTP(rec, sub)
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}
	return status, rec.buf.Bytes()
}

func rpcErr(id json.RawMessage, code int, msg string) jsonrpcResponse {
	return jsonrpcResponse{JSONRPC: "2.0", ID: id, Error: &jsonrpcError{Code: code, Message: msg}}
}

// mcpRecorder is a minimal in-memory http.ResponseWriter for in-process
// dispatch, avoiding a dependency on net/http/httptest in library code.
type mcpRecorder struct {
	status int
	buf    bytes.Buffer
	header http.Header
}

func (r *mcpRecorder) Header() http.Header {
	if r.header == nil {
		r.header = http.Header{}
	}
	return r.header
}

func (r *mcpRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.buf.Write(b)
}

func (r *mcpRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}
