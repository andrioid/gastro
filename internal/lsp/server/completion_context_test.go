package server

import "testing"

func TestIsInsideAction(t *testing.T) {
	tests := []struct {
		name     string
		template string
		cursor   int
		want     bool
	}{
		{
			name:     "inside simple action after opening",
			template: "{{ .Title }}",
			cursor:   3,
			want:     true,
		},
		{
			name:     "right after opening delimiter",
			template: "{{ .Title }}",
			cursor:   2,
			want:     true,
		},
		{
			name:     "at end of action content",
			template: "{{ .Title }}",
			cursor:   10,
			want:     true,
		},
		{
			name:     "outside action before",
			template: "Hello {{ .Title }}",
			cursor:   3,
			want:     false,
		},
		{
			name:     "outside action after",
			template: "{{ .Title }} World",
			cursor:   15,
			want:     false,
		},
		{
			name:     "single brace in CSS",
			template: "<style>.class { color: red; }</style>",
			cursor:   16,
			want:     false,
		},
		{
			name:     "multiline action",
			template: "{{\n  .Title\n}}",
			cursor:   6,
			want:     true,
		},
		{
			name:     "between two actions",
			template: "{{ .A }} hello {{ .B }}",
			cursor:   10,
			want:     false,
		},
		{
			name:     "inside second action",
			template: "{{ .A }} hello {{ .B }}",
			cursor:   17,
			want:     true,
		},
		{
			name:     "empty template body",
			template: "",
			cursor:   0,
			want:     false,
		},
		{
			name:     "negative offset",
			template: "{{ .Title }}",
			cursor:   -1,
			want:     false,
		},
		{
			name:     "offset beyond template",
			template: "{{ .Title }}",
			cursor:   100,
			want:     false,
		},
		{
			name:     "pipe expression",
			template: "{{ .Items | len }}",
			cursor:   12,
			want:     true,
		},
		{
			name:     "range block opening",
			template: "{{ range .Items }}",
			cursor:   5,
			want:     true,
		},
		{
			name:     "inside range body between actions",
			template: "{{ range .Items }}<li>{{ .Name }}</li>{{ end }}",
			cursor:   20,
			want:     false,
		},
		{
			name:     "inside nested action in range body",
			template: "{{ range .Items }}<li>{{ .Name }}</li>{{ end }}",
			cursor:   24,
			want:     true,
		},
		{
			name:     "just opened double brace",
			template: "<div>{{",
			cursor:   7,
			want:     true,
		},
		{
			name:     "single brace only",
			template: "<div>{",
			cursor:   6,
			want:     false,
		},
		{
			name:     "JS object literal",
			template: `<script>const x = { name: "test" };</script>`,
			cursor:   22,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isInsideAction(tt.template, tt.cursor)
			if got != tt.want {
				t.Errorf("isInsideAction(%q, %d) = %v, want %v",
					tt.template, tt.cursor, got, tt.want)
			}
		})
	}
}
