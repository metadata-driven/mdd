package meta

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/goldic/js"
)

func parseConfig(t *testing.T, source string) Config {
	t.Helper()
	cfg, err := ParseYAML([]byte(source))
	require(t, err == nil, "ParseYAML() error = %v", err)
	require(t, cfg != nil, "ParseYAML() returned a nil configuration")
	return cfg
}

func lookupNodeKind(t *testing.T, cfg Config, path string, kind int) *node {
	t.Helper()
	node := lookupNode(t, cfg, path)
	assertEqual(t, node.Kind(), kind)
	return node
}

func lookupNode(t *testing.T, cfg Config, path string) *node {
	t.Helper()
	value := cfg.Lookup(path)
	require(t, value != nil, "Lookup(%q) returned nil", path)
	result, ok := value.(*node)
	require(t, ok, "Lookup(%q) returned %T, want *node", path, value)
	return result
}

func assertEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	require(t, got == want, "got %v, want %v", got, want)
}

func assertNode(t *testing.T, node Node, kind int, name, path string) {
	t.Helper()
	require(t, node != nil, "node %q is nil", path)
	assertEqual(t, node.Kind(), kind)
	assertEqual(t, node.Name(), name)
	assertEqual(t, node.Path(), path)
}

func assertNames[T interface{ Name() string }](t *testing.T, nodes []T, want ...string) {
	t.Helper()
	got := make([]string, len(nodes))
	for i, node := range nodes {
		got[i] = node.Name()
	}
	require(t, slices.Equal(got, want), "node names = %v, want %v", got, want)
}

func assertSameNode(t *testing.T, got, want Node) {
	t.Helper()
	require(t, got == want, "got node %v, want %v", got, want)
}

func assertSameSlice[T any](t *testing.T, got, want []T) {
	t.Helper()
	require(t, len(got) == len(want), "slice lengths differ: got %d, want %d", len(got), len(want))
	if len(got) > 0 {
		require(t, &got[0] == &want[0], "slices have different backing arrays")
	}
}

func rawValue(node Node, path string) js.Value {
	value := js.NewValue(node.Raw())
	for _, part := range strings.Split(path, ".") {
		if value.IsArray() {
			index, err := strconv.Atoi(part)
			if err != nil {
				return js.Value{}
			}
			value = value.Array().Eq(index)
		} else {
			value = value.Object().Get(part)
		}
	}
	return value
}

func metadataNodes(t *testing.T, root Node) []*node {
	t.Helper()
	rootNode, ok := root.(*node)
	require(t, ok, "root has concrete type %T, want *node", root)
	queue := []*node{rootNode}
	seen := make(map[string]bool)
	for i := 0; i < len(queue); i++ {
		current := queue[i]
		require(t, !seen[current.Path()], "duplicate or cyclic child path %q", current.Path())
		seen[current.Path()] = true
		assertSameNode(t, current.Root(), root)
		assertSameNode(t, root.Lookup(current.Path()), current)
		for _, child := range current.Children() {
			assertSameNode(t, child.Parent(), current)
			childNode, ok := child.(*node)
			require(t, ok, "%s has concrete type %T, want *node", child.Path(), child)
			queue = append(queue, childNode)
		}
	}
	return queue
}

func assertCached(t *testing.T, value Node, keys ...int) {
	t.Helper()
	node := value.(*node)
	for _, key := range keys {
		_, ok := node.cache[key]
		require(t, ok, "%s cache does not contain key %d", node.Path(), key)
	}
}

func require(t *testing.T, ok bool, format string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(format, args...)
	}
}
