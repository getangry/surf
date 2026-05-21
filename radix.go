package surf

import (
	"strings"
)

// nodeType represents the type of a radix tree node
type nodeType uint8

const (
	staticNode   nodeType = iota // Regular static path segment
	paramNode                    // Parameter segment (:id)
	wildcardNode                 // Wildcard segment (*)
)

// radixNode represents a node in the radix tree
type radixNode struct {
	path     string       // The path segment this node represents
	nodeType nodeType     // Type of node
	handler  *route       // Handler for this route (nil if not a terminal node)
	children []*radixNode // Child nodes
	paramKey string       // Parameter name for param nodes
}

// radixTree is a radix tree for efficient route matching
type radixTree struct {
	root *radixNode
}

// newRadixTree creates a new radix tree
func newRadixTree() *radixTree {
	return &radixTree{
		root: &radixNode{
			path:     "",
			nodeType: staticNode,
			children: make([]*radixNode, 0),
		},
	}
}

// insert adds a route to the radix tree
func (t *radixTree) insert(pattern string, handler *route) {
	if pattern == "" {
		pattern = "/"
	}

	current := t.root
	remaining := pattern

	for len(remaining) > 0 {
		// Check for parameter or wildcard
		if remaining[0] == ':' {
			// Find end of parameter name
			end := strings.IndexByte(remaining[1:], '/')
			var paramName string
			if end == -1 {
				paramName = remaining[1:]
				remaining = ""
			} else {
				paramName = remaining[1 : end+1]
				remaining = remaining[end+1:]
			}

			// Look for existing param child
			var paramChild *radixNode
			for _, child := range current.children {
				if child.nodeType == paramNode {
					paramChild = child
					break
				}
			}

			if paramChild == nil {
				paramChild = &radixNode{
					path:     ":",
					nodeType: paramNode,
					paramKey: paramName,
					children: make([]*radixNode, 0),
				}
				current.children = append(current.children, paramChild)
			}
			current = paramChild
			continue
		}

		if remaining[0] == '*' {
			// Wildcard matches everything after
			wildcardChild := &radixNode{
				path:     "*",
				nodeType: wildcardNode,
				paramKey: "*",
				handler:  handler,
				children: make([]*radixNode, 0),
			}
			current.children = append(current.children, wildcardChild)
			return
		}

		// Static segment - find common prefix with existing children
		matched := false
		for _, child := range current.children {
			if child.nodeType != staticNode {
				continue
			}

			commonLen := longestCommonPrefix(remaining, child.path)
			if commonLen == 0 {
				continue
			}

			matched = true

			if commonLen == len(child.path) {
				// Child path is fully matched, continue with remaining
				remaining = remaining[commonLen:]
				current = child
				break
			}

			// Need to split the child node
			// Create a new node for the common prefix
			splitNode := &radixNode{
				path:     child.path[:commonLen],
				nodeType: staticNode,
				children: make([]*radixNode, 0),
			}

			// Update original child with remaining path
			child.path = child.path[commonLen:]
			splitNode.children = append(splitNode.children, child)

			// Replace child with split node in parent
			for i, c := range current.children {
				if c == child {
					current.children[i] = splitNode
					break
				}
			}

			remaining = remaining[commonLen:]
			current = splitNode
			break
		}

		if !matched {
			// No matching child, create new static node
			// Find next param/wildcard or end
			end := len(remaining)
			for i := 0; i < len(remaining); i++ {
				if remaining[i] == ':' || remaining[i] == '*' {
					end = i
					break
				}
			}

			newNode := &radixNode{
				path:     remaining[:end],
				nodeType: staticNode,
				children: make([]*radixNode, 0),
			}
			current.children = append(current.children, newNode)
			remaining = remaining[end:]
			current = newNode
		}
	}

	current.handler = handler
}

// search finds a route in the radix tree
func (t *radixTree) search(path string) (*route, map[string]string) {
	if path == "" {
		path = "/"
	}

	params := make(map[string]string)
	route := t.searchNode(t.root, path, params)
	if route == nil {
		return nil, nil
	}
	return route, params
}

// searchNode recursively searches the tree
func (t *radixTree) searchNode(node *radixNode, path string, params map[string]string) *route {
	if len(path) == 0 {
		return node.handler
	}

	// Try static children first (more specific)
	for _, child := range node.children {
		if child.nodeType == staticNode {
			if strings.HasPrefix(path, child.path) {
				result := t.searchNode(child, path[len(child.path):], params)
				if result != nil {
					return result
				}
			}
		}
	}

	// Try parameter children
	for _, child := range node.children {
		if child.nodeType == paramNode {
			// Find end of this path segment
			end := strings.IndexByte(path, '/')
			var value string
			var remaining string
			if end == -1 {
				value = path
				remaining = ""
			} else {
				value = path[:end]
				remaining = path[end:]
			}

			if value != "" {
				params[child.paramKey] = value
				result := t.searchNode(child, remaining, params)
				if result != nil {
					return result
				}
				delete(params, child.paramKey)
			}
		}
	}

	// Try wildcard children last
	for _, child := range node.children {
		if child.nodeType == wildcardNode {
			params["*"] = path
			return child.handler
		}
	}

	return nil
}

// searchKV is the allocation-free variant of search used on the hot path. It
// resets *params and appends each matched parameter to it, reusing the slice's
// backing array across requests.
func (t *radixTree) searchKV(path string, params *[]paramKV) *route {
	if path == "" {
		path = "/"
	}
	*params = (*params)[:0]
	return t.searchNodeKV(t.root, path, params)
}

// searchNodeKV mirrors searchNode but records parameters in a slice. On a
// failed branch it truncates the slice back to the pre-branch length.
func (t *radixTree) searchNodeKV(node *radixNode, path string, params *[]paramKV) *route {
	if len(path) == 0 {
		return node.handler
	}

	// Static children first (most specific).
	for _, child := range node.children {
		if child.nodeType == staticNode && strings.HasPrefix(path, child.path) {
			if r := t.searchNodeKV(child, path[len(child.path):], params); r != nil {
				return r
			}
		}
	}

	// Parameter children.
	for _, child := range node.children {
		if child.nodeType != paramNode {
			continue
		}
		end := strings.IndexByte(path, '/')
		var value, remaining string
		if end == -1 {
			value, remaining = path, ""
		} else {
			value, remaining = path[:end], path[end:]
		}
		if value == "" {
			continue
		}
		mark := len(*params)
		*params = append(*params, paramKV{key: child.paramKey, val: value})
		if r := t.searchNodeKV(child, remaining, params); r != nil {
			return r
		}
		*params = (*params)[:mark]
	}

	// Wildcard children last.
	for _, child := range node.children {
		if child.nodeType == wildcardNode {
			*params = append(*params, paramKV{key: "*", val: path})
			return child.handler
		}
	}

	return nil
}

// longestCommonPrefix finds the longest common prefix between two strings
func longestCommonPrefix(a, b string) int {
	max := len(a)
	if len(b) < max {
		max = len(b)
	}

	for i := 0; i < max; i++ {
		if a[i] != b[i] || a[i] == ':' || a[i] == '*' {
			return i
		}
	}
	return max
}
