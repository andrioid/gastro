package server

import (
	"encoding/json"
	"testing"
)

func TestFindDotStart(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		line      int
		character int
		want      int
	}{
		{
			name:      "cursor after .T in template expression",
			content:   "<h1>{{ .T }}</h1>",
			line:      0,
			character: 9, // after ".T"
			want:      7, // position of '.'
		},
		{
			name:      "cursor at start of line",
			content:   "<h1>{{ .Title }}</h1>",
			line:      0,
			character: 0,
			want:      -1,
		},
		{
			name:      "no dot before cursor",
			content:   "<h1>hello</h1>",
			line:      0,
			character: 5,
			want:      -1,
		},
		{
			name:      "line out of range negative",
			content:   "hello",
			line:      -1,
			character: 3,
			want:      -1,
		},
		{
			name:      "line out of range too high",
			content:   "hello",
			line:      5,
			character: 3,
			want:      -1,
		},
		{
			name:      "dot found on second line",
			content:   "line one\n{{ .Title }}",
			line:      1,
			character: 5, // after ".T"
			want:      3, // position of '.' on line 1
		},
		{
			name:      "stops at space before dot",
			content:   "{{ .Title }}",
			line:      0,
			character: 5, // after ".Tit" but cursor is at "i"
			want:      3, // position of '.'
		},
		{
			name:      "stops at non-identifier character",
			content:   "{{ | .Title }}",
			line:      0,
			character: 4, // after "{{ | "
			want:      -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findDotStart(tt.content, tt.line, tt.character)
			if got != tt.want {
				t.Errorf("findDotStart(%q, %d, %d) = %d, want %d",
					tt.content, tt.line, tt.character, got, tt.want)
			}
		})
	}
}

func TestParseTypeFromHover(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "gopls var declaration",
			raw:  "{\"contents\":{\"kind\":\"markdown\",\"value\":\"```go\\nvar Posts []db.Post\\n```\"}}",
			want: "[]db.Post",
		},
		{
			name: "gopls short assignment",
			raw:  "{\"contents\":{\"kind\":\"markdown\",\"value\":\"```go\\nPosts []db.Post\\n```\"}}",
			want: "[]db.Post",
		},
		{
			name: "simple string type",
			raw:  "{\"contents\":{\"kind\":\"markdown\",\"value\":\"```go\\nvar Title string\\n```\"}}",
			want: "string",
		},
		{
			name: "pointer type",
			raw:  "{\"contents\":{\"kind\":\"markdown\",\"value\":\"```go\\nvar Post *db.Post\\n```\"}}",
			want: "*db.Post",
		},
		{
			name: "empty response",
			raw:  "{\"contents\":{\"kind\":\"markdown\",\"value\":\"\"}}",
			want: "",
		},
		{
			name: "null contents",
			raw:  "{}",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTypeFromHover(json.RawMessage(tt.raw))
			if got != tt.want {
				t.Errorf("parseTypeFromHover() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestElementTypeFromContainer(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"[]db.Post", "db.Post"},
		{"[]*db.Post", "*db.Post"},
		{"[]string", "string"},
		{"map[string]db.Post", "db.Post"},
		{"[5]int", "int"},
		{"string", ""},
		{"int", ""},
		{"*db.Post", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := elementTypeFromContainer(tt.input)
			if got != tt.want {
				t.Errorf("elementTypeFromContainer(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
