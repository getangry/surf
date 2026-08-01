package surf

import (
	"net/http"
	"strconv"
	"strings"
)

// APIInfo is the metadata block of a generated OpenAPI document.
type APIInfo struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
}

// OpenAPIDoc is a generated OpenAPI 3.1 document. It is produced from the
// registered routes and is safe to marshal directly to JSON.
type OpenAPIDoc struct {
	OpenAPI    string               `json:"openapi"`
	Info       APIInfo              `json:"info"`
	Paths      map[string]*PathItem `json:"paths"`
	Components *Components          `json:"components,omitempty"`
}

// PathItem groups the operations registered on a single path.
type PathItem struct {
	Get     *Operation `json:"get,omitempty"`
	Post    *Operation `json:"post,omitempty"`
	Put     *Operation `json:"put,omitempty"`
	Delete  *Operation `json:"delete,omitempty"`
	Patch   *Operation `json:"patch,omitempty"`
	Head    *Operation `json:"head,omitempty"`
	Options *Operation `json:"options,omitempty"`
}

// set assigns op to the field matching method. Unknown methods are ignored.
func (p *PathItem) set(method string, op *Operation) {
	switch method {
	case http.MethodGet:
		p.Get = op
	case http.MethodPost:
		p.Post = op
	case http.MethodPut:
		p.Put = op
	case http.MethodDelete:
		p.Delete = op
	case http.MethodPatch:
		p.Patch = op
	case http.MethodHead:
		p.Head = op
	case http.MethodOptions:
		p.Options = op
	}
}

// Operation describes a single method on a path.
type Operation struct {
	Summary     string               `json:"summary,omitempty"`
	Description string               `json:"description,omitempty"`
	OperationID string               `json:"operationId,omitempty"`
	Parameters  []Parameter          `json:"parameters,omitempty"`
	RequestBody *RequestBody         `json:"requestBody,omitempty"`
	Responses   map[string]*Response `json:"responses"`
}

// Parameter describes a path or query parameter.
type Parameter struct {
	Name     string  `json:"name"`
	In       string  `json:"in"`
	Required bool    `json:"required"`
	Schema   *Schema `json:"schema,omitempty"`
}

// RequestBody describes a JSON request body.
type RequestBody struct {
	Required bool                 `json:"required,omitempty"`
	Content  map[string]MediaType `json:"content"`
}

// Response describes a single response by status code.
type Response struct {
	Description string               `json:"description"`
	Content     map[string]MediaType `json:"content,omitempty"`
}

// MediaType pairs a content type with its schema.
type MediaType struct {
	Schema *Schema `json:"schema,omitempty"`
}

// Components holds the reusable schemas referenced by $ref throughout the
// document.
type Components struct {
	Schemas map[string]*Schema `json:"schemas,omitempty"`
}

const openAPIComponentsBase = "#/components/schemas/"

// OpenAPI builds an OpenAPI 3.1 document from the app's registered routes.
//
// Typed routes (HandleJSON/HandleJSONStatus/HandleQuery) contribute full
// request and response schemas, derived by reflection over the captured Req and
// Resp types. Routes registered with Get/Post/Handle contribute their method,
// path, and path parameters, with a free-form response — the framework cannot
// know their body shape, so the document degrades gracefully rather than
// guessing.
func (app *App) OpenAPI(info APIInfo) *OpenAPIDoc {
	if info.Version == "" {
		info.Version = "0.0.0"
	}
	doc := &OpenAPIDoc{
		OpenAPI: "3.1.0",
		Info:    info,
		Paths:   map[string]*PathItem{},
	}
	b := newSchemaBuilder(openAPIComponentsBase)

	for _, ri := range app.Routes() {
		path := openAPIPath(ri.Pattern)
		item := doc.Paths[path]
		if item == nil {
			item = &PathItem{}
			doc.Paths[path] = item
		}
		item.set(ri.Method, buildOperation(ri, b))
	}

	if len(b.Defs) > 0 {
		doc.Components = &Components{Schemas: b.Defs}
	}
	return doc
}

// OpenAPIHandler serves the generated document as JSON. The document is built
// once, on the first request, and cached — register routes before mounting it.
//
//	app.Get("/openapi.json", app.OpenAPIHandler(surf.APIInfo{Title: "My API", Version: "1.0.0"}))
func (app *App) OpenAPIHandler(info APIInfo) HandlerFunc {
	var cached *OpenAPIDoc
	return func(w http.ResponseWriter, r *http.Request) error {
		if cached == nil {
			cached = app.OpenAPI(info)
		}
		return JSON(w, http.StatusOK, cached)
	}
}

// buildOperation turns a single RouteInfo into an OpenAPI operation, drawing
// request/response schemas from the shared builder so their nested types land
// in components.schemas.
func buildOperation(ri RouteInfo, b *schemaBuilder) *Operation {
	op := &Operation{
		Summary:     ri.Method + " " + ri.Pattern,
		OperationID: operationID(ri.Method, ri.Pattern),
		Responses:   map[string]*Response{},
	}

	for _, name := range ri.Params {
		op.Parameters = append(op.Parameters, Parameter{
			Name:     name,
			In:       "path",
			Required: true,
			Schema:   &Schema{Type: "string"},
		})
	}

	if ri.ReqType != nil {
		op.RequestBody = &RequestBody{
			Required: true,
			Content: map[string]MediaType{
				"application/json": {Schema: b.build(ri.ReqType)},
			},
		}
	}

	status := ri.SuccessStatus
	if status == 0 {
		status = http.StatusOK
	}
	resp := &Response{Description: http.StatusText(status)}
	if ri.RespType != nil {
		resp.Content = map[string]MediaType{
			"application/json": {Schema: b.build(ri.RespType)},
		}
	}
	op.Responses[strconv.Itoa(status)] = resp
	return op
}

// openAPIPath rewrites Surf's ":param" path syntax into OpenAPI's "{param}".
func openAPIPath(pattern string) string {
	if !strings.Contains(pattern, ":") {
		return pattern
	}
	parts := strings.Split(pattern, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, ":") {
			parts[i] = "{" + p[1:] + "}"
		}
	}
	return strings.Join(parts, "/")
}

// operationID builds a stable, unique-ish id from method and pattern, e.g.
// "get_users_id" for "GET /users/:id".
func operationID(method, pattern string) string {
	s := strings.ToLower(method) + pattern
	var b strings.Builder
	b.Grow(len(s))
	prevUnderscore := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if !prevUnderscore {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}
