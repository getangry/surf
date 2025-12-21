package surf

import (
	"testing"
)

func TestRadixTreeBasic(t *testing.T) {
	tree := newRadixTree()

	routes := []string{
		"/",
		"/users",
		"/users/list",
		"/posts",
		"/posts/new",
	}

	for _, pattern := range routes {
		tree.insert(pattern, &route{pattern: pattern})
	}

	for _, pattern := range routes {
		route, params := tree.search(pattern)
		if route == nil {
			t.Errorf("route %q not found", pattern)
			continue
		}
		if route.pattern != pattern {
			t.Errorf("route pattern = %q, want %q", route.pattern, pattern)
		}
		if len(params) != 0 {
			t.Errorf("expected no params, got %v", params)
		}
	}
}

func TestRadixTreeParameters(t *testing.T) {
	tree := newRadixTree()

	tree.insert("/users/:id", &route{pattern: "/users/:id"})
	tree.insert("/users/:id/posts/:postId", &route{pattern: "/users/:id/posts/:postId"})
	tree.insert("/files/:type/:name", &route{pattern: "/files/:type/:name"})

	tests := []struct {
		path   string
		want   string
		params map[string]string
	}{
		{"/users/123", "/users/:id", map[string]string{"id": "123"}},
		{"/users/abc", "/users/:id", map[string]string{"id": "abc"}},
		{"/users/42/posts/99", "/users/:id/posts/:postId", map[string]string{"id": "42", "postId": "99"}},
		{"/files/images/cat.jpg", "/files/:type/:name", map[string]string{"type": "images", "name": "cat.jpg"}},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			route, params := tree.search(tt.path)
			if route == nil {
				t.Fatalf("route not found for %q", tt.path)
			}
			if route.pattern != tt.want {
				t.Errorf("pattern = %q, want %q", route.pattern, tt.want)
			}
			for k, v := range tt.params {
				if params[k] != v {
					t.Errorf("params[%q] = %q, want %q", k, params[k], v)
				}
			}
		})
	}
}

func TestRadixTreeWildcard(t *testing.T) {
	tree := newRadixTree()

	tree.insert("/static/*", &route{pattern: "/static/*"})
	tree.insert("/files/:type/*", &route{pattern: "/files/:type/*"})

	tests := []struct {
		path   string
		want   string
		params map[string]string
	}{
		{"/static/css/style.css", "/static/*", map[string]string{"*": "css/style.css"}},
		{"/static/js/app.js", "/static/*", map[string]string{"*": "js/app.js"}},
		{"/static/deep/nested/path/file.txt", "/static/*", map[string]string{"*": "deep/nested/path/file.txt"}},
		{"/files/images/photos/cat.jpg", "/files/:type/*", map[string]string{"type": "images", "*": "photos/cat.jpg"}},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			route, params := tree.search(tt.path)
			if route == nil {
				t.Fatalf("route not found for %q", tt.path)
			}
			if route.pattern != tt.want {
				t.Errorf("pattern = %q, want %q", route.pattern, tt.want)
			}
			for k, v := range tt.params {
				if params[k] != v {
					t.Errorf("params[%q] = %q, want %q", k, params[k], v)
				}
			}
		})
	}
}

func TestRadixTreeNotFound(t *testing.T) {
	tree := newRadixTree()

	tree.insert("/users", &route{pattern: "/users"})
	tree.insert("/users/:id", &route{pattern: "/users/:id"})

	notFound := []string{
		"/posts",
		"/user",
		"/userss",
		"/users/123/extra",
	}

	for _, path := range notFound {
		t.Run(path, func(t *testing.T) {
			route, _ := tree.search(path)
			if route != nil {
				t.Errorf("expected nil for %q, got %q", path, route.pattern)
			}
		})
	}
}

func TestRadixTreePriority(t *testing.T) {
	tree := newRadixTree()

	// Static routes should take priority over parameter routes
	tree.insert("/users/new", &route{pattern: "/users/new"})
	tree.insert("/users/:id", &route{pattern: "/users/:id"})

	// /users/new should match static, not param
	route, params := tree.search("/users/new")
	if route == nil {
		t.Fatal("route not found")
	}
	if route.pattern != "/users/new" {
		t.Errorf("pattern = %q, want /users/new", route.pattern)
	}
	if len(params) != 0 {
		t.Errorf("expected no params for static route, got %v", params)
	}

	// /users/123 should match param
	route, params = tree.search("/users/123")
	if route == nil {
		t.Fatal("route not found")
	}
	if route.pattern != "/users/:id" {
		t.Errorf("pattern = %q, want /users/:id", route.pattern)
	}
	if params["id"] != "123" {
		t.Errorf("params[id] = %q, want 123", params["id"])
	}
}

func TestRadixTreeCommonPrefix(t *testing.T) {
	tree := newRadixTree()

	// Routes with common prefixes
	tree.insert("/api/users", &route{pattern: "/api/users"})
	tree.insert("/api/posts", &route{pattern: "/api/posts"})
	tree.insert("/api/users/list", &route{pattern: "/api/users/list"})
	tree.insert("/app/settings", &route{pattern: "/app/settings"})

	tests := []string{
		"/api/users",
		"/api/posts",
		"/api/users/list",
		"/app/settings",
	}

	for _, path := range tests {
		t.Run(path, func(t *testing.T) {
			route, _ := tree.search(path)
			if route == nil {
				t.Fatalf("route not found for %q", path)
			}
			if route.pattern != path {
				t.Errorf("pattern = %q, want %q", route.pattern, path)
			}
		})
	}
}

func TestLongestCommonPrefix(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"abc", "abd", 2},
		{"abc", "abc", 3},
		{"abc", "xyz", 0},
		{"", "abc", 0},
		{"abc", "", 0},
		{"/users", "/users/list", 6},
		{"/api:id", "/api/v1", 4}, // stops at :
	}

	for _, tt := range tests {
		t.Run(tt.a+"|"+tt.b, func(t *testing.T) {
			got := longestCommonPrefix(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("longestCommonPrefix(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func BenchmarkRadixTreeSearch(b *testing.B) {
	tree := newRadixTree()

	// Add many routes
	routes := []string{
		"/",
		"/users",
		"/users/:id",
		"/users/:id/posts",
		"/users/:id/posts/:postId",
		"/api/v1/users",
		"/api/v1/posts",
		"/api/v2/users",
		"/api/v2/posts",
		"/static/*",
	}

	for _, pattern := range routes {
		tree.insert(pattern, &route{pattern: pattern})
	}

	searchPaths := []string{
		"/",
		"/users",
		"/users/123",
		"/users/456/posts/789",
		"/api/v1/users",
		"/static/css/style.css",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, path := range searchPaths {
			tree.search(path)
		}
	}
}

func BenchmarkRadixVsLinear(b *testing.B) {
	// Create 100 routes
	routes := make([]string, 100)
	for i := 0; i < 100; i++ {
		routes[i] = "/api/v1/resource" + string(rune('a'+i%26)) + "/" + string(rune('0'+i%10))
	}

	b.Run("Radix", func(b *testing.B) {
		tree := newRadixTree()
		for _, pattern := range routes {
			tree.insert(pattern, &route{pattern: pattern})
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tree.search(routes[50]) // Search middle route
		}
	})

	b.Run("Linear", func(b *testing.B) {
		routeMap := make(map[string]*route)
		for _, pattern := range routes {
			routeMap[pattern] = &route{pattern: pattern}
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Simulate linear search
			for pattern, r := range routeMap {
				if _, ok := matchPath(pattern, routes[50]); ok {
					_ = r
					break
				}
			}
		}
	})
}
