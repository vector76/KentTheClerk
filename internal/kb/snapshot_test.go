package kb

import (
	"testing"

	"kenttheclerk/internal/py"
)

// A tool call is summarized by its description, or failing that by a path, pattern, query or URL; a
// command or prompt never enters the record under the default filter.
func TestToolCallSummaryLeavesOutCommandsAndPrompts(t *testing.T) {
	cases := []struct {
		name  string
		input *py.Dict
		want  any
	}{
		{"described", py.D("command", "curl -H 'Authorization: Bearer abc'", "description", "Fetch the page"), "Fetch the page"},
		{"command", py.D("command", "export API_KEY=abc"), nil},
		{"prompt", py.D("prompt", "Use the token abc"), nil},
		{"command and path", py.D("command", "export API_KEY=abc", "file_path", "notes.md"), "notes.md"},
		{"pattern", py.D("pattern", "TODO"), "TODO"},
		{"url", py.D("url", "http://127.0.0.1:8765/"), "http://127.0.0.1:8765/"},
	}
	for _, c := range cases {
		block := py.D("type", "tool_use", "id", "t1", "name", "Bash", "input", c.input)
		got, _ := toolCallSummary(block, "human-visible-v1").Lookup("description")
		if got != c.want {
			t.Errorf("%s: description %v, want %v", c.name, got, c.want)
		}
		full := toolCallSummary(block, "full-v1")
		if !full.Has("input") {
			t.Errorf("%s: full-v1 dropped the input", c.name)
		}
	}
}
