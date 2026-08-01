package surf

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mcpCreateReq struct {
	MCPRequest `name:"create_user" desc:"Create a user"`
	Name       string `json:"name" desc:"Full name" required:"true"`
}

// Validate enforces the required field at runtime. The required:"true" tag only
// drives schema generation; runtime enforcement is the Validator interface's
// job, exactly as with any other typed handler.
func (r mcpCreateReq) Validate() error {
	if r.Name == "" {
		return errValidation
	}
	return nil
}

var errValidation = &HTTPError{Code: http.StatusUnprocessableEntity, Message: "name is required"}

type mcpUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// rpc sends a JSON-RPC request to the mounted MCP endpoint and returns the
// decoded response.
func rpc(t *testing.T, app *App, body string) jsonrpcResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(rec, req)
	var resp jsonrpcResponse
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("bad JSON-RPC response %q: %v", rec.Body.String(), err)
		}
	}
	return resp
}

func newMCPApp(t *testing.T, opts MCPOptions) *App {
	t.Helper()
	app := NewApp()
	MCPHandle(app, "POST", "/teams/:team/users",
		func(c *Context, req mcpCreateReq) (mcpUser, error) {
			// Prove path params propagate through in-process dispatch.
			return mcpUser{ID: c.Param("team"), Name: req.Name}, nil
		})
	app.MCP("/mcp", opts)
	return app
}

func TestMCPInitialize(t *testing.T) {
	app := newMCPApp(t, MCPOptions{ServerName: "surf-test", ServerVersion: "9.9"})
	resp := rpc(t, app, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	if result["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v", result["protocolVersion"])
	}
	info := result["serverInfo"].(map[string]any)
	if info["name"] != "surf-test" || info["version"] != "9.9" {
		t.Errorf("serverInfo = %v", info)
	}
}

func TestMCPToolsList(t *testing.T) {
	app := newMCPApp(t, MCPOptions{})
	resp := rpc(t, app, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}
	tools := resp.Result.(map[string]any)["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "create_user" || tool["description"] != "Create a user" {
		t.Errorf("tool meta wrong: %v", tool)
	}
	schema := tool["inputSchema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	// Both the path param and the body field appear as arguments.
	if props["team"] == nil {
		t.Error("inputSchema missing path param 'team'")
	}
	if props["name"] == nil {
		t.Error("inputSchema missing body field 'name'")
	}
	required := toStringSet(schema["required"])
	if !required["team"] || !required["name"] {
		t.Errorf("required = %v, want team+name", schema["required"])
	}
}

func TestMCPToolsCall(t *testing.T) {
	app := newMCPApp(t, MCPOptions{})
	resp := rpc(t, app, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_user","arguments":{"team":"eng","name":"Ada"}}}`)
	if resp.Error != nil {
		t.Fatalf("tools/call error: %+v", resp.Error)
	}
	result := resp.Result.(map[string]any)
	if result["isError"] == true {
		t.Fatalf("unexpected isError: %v", result)
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)

	var user mcpUser
	if err := json.Unmarshal([]byte(text), &user); err != nil {
		t.Fatalf("tool result not JSON: %q", text)
	}
	if user.ID != "eng" { // came from the :team path param
		t.Errorf("path param not propagated: ID = %q, want eng", user.ID)
	}
	if user.Name != "Ada" { // came from the JSON body
		t.Errorf("body field not propagated: Name = %q", user.Name)
	}
}

func TestMCPToolsCallValidationError(t *testing.T) {
	// Missing required body field flows through the real bind pipeline as a 4xx,
	// surfaced as isError:true.
	app := newMCPApp(t, MCPOptions{})
	resp := rpc(t, app, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_user","arguments":{"team":"eng"}}}`)
	if resp.Error != nil {
		t.Fatalf("transport-level error: %+v", resp.Error)
	}
	// The empty body is a bad request; the tool result marks it an error.
	if resp.Result.(map[string]any)["isError"] != true {
		t.Errorf("expected isError:true for empty body, got %v", resp.Result)
	}
}

func TestMCPExposeWhenHidesTool(t *testing.T) {
	app := newMCPApp(t, MCPOptions{
		ExposeWhen: func(c *Context, tool MCPToolInfo) bool {
			return c.Header("X-Admin") == "1"
		},
	})

	// Without the header, the tool is invisible and uncallable.
	list := rpc(t, app, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if n := len(list.Result.(map[string]any)["tools"].([]any)); n != 0 {
		t.Errorf("unauthorized tools/list should be empty, got %d", n)
	}
	call := rpc(t, app, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"create_user","arguments":{"team":"eng","name":"Ada"}}}`)
	if call.Error == nil {
		t.Error("unauthorized tools/call should error")
	}

	// With the header, it appears.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`))
	req.Header.Set("X-Admin", "1")
	app.ServeHTTP(rec, req)
	var resp jsonrpcResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if n := len(resp.Result.(map[string]any)["tools"].([]any)); n != 1 {
		t.Errorf("authorized tools/list should show 1 tool, got %d", n)
	}
}

func TestMCPUnknownMethod(t *testing.T) {
	app := newMCPApp(t, MCPOptions{})
	resp := rpc(t, app, `{"jsonrpc":"2.0","id":1,"method":"bogus"}`)
	if resp.Error == nil || resp.Error.Code != rpcMethodNotFound {
		t.Errorf("want method-not-found, got %+v", resp.Error)
	}
}

func TestMCPNotificationNoResponse(t *testing.T) {
	app := newMCPApp(t, MCPOptions{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	req.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Errorf("notification status = %d, want 202", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("notification should have empty body, got %q", rec.Body.String())
	}
}

func toStringSet(v any) map[string]bool {
	out := map[string]bool{}
	if arr, ok := v.([]any); ok {
		for _, e := range arr {
			if s, ok := e.(string); ok {
				out[s] = true
			}
		}
	}
	return out
}
