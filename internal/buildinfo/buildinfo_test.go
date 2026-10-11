package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestFromBuildInfo(t *testing.T) {
	tests := []struct {
		name string
		bi   debug.BuildInfo
		want string
	}{
		{
			name: "go install of a release",
			bi:   debug.BuildInfo{Main: debug.Module{Path: Module, Version: "v0.4.0"}},
			want: "v0.4.0",
		},
		{
			name: "local build in the gastro repo",
			bi:   debug.BuildInfo{Main: debug.Module{Path: Module, Version: "(devel)"}},
			want: "(devel)",
		},
		{
			name: "go tool gastro in a consumer module",
			bi: debug.BuildInfo{
				Main: debug.Module{Path: "example.com/app", Version: "(devel)"},
				Deps: []*debug.Module{
					{Path: "github.com/google/shlex", Version: "v0.0.0-20191202100458-e7afc7fbc510"},
					{Path: Module, Version: "v0.4.1"},
				},
			},
			want: "v0.4.1",
		},
		{
			name: "consumer replaces gastro with a local checkout",
			bi: debug.BuildInfo{
				Main: debug.Module{Path: "example.com/app"},
				Deps: []*debug.Module{
					{Path: Module, Version: "v0.0.0", Replace: &debug.Module{Path: "../gastro"}},
				},
			},
			want: Devel,
		},
		{
			name: "consumer replaces gastro with a fork version",
			bi: debug.BuildInfo{
				Main: debug.Module{Path: "example.com/app"},
				Deps: []*debug.Module{
					{Path: Module, Version: "v0.4.0", Replace: &debug.Module{Path: "example.com/fork/gastro", Version: "v0.4.0-fork.1"}},
				},
			},
			want: "v0.4.0-fork.1",
		},
		{
			name: "gastro absent",
			bi:   debug.BuildInfo{Main: debug.Module{Path: "example.com/other", Version: "v1.0.0"}},
			want: Devel,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fromBuildInfo(&tt.bi); got != tt.want {
				t.Errorf("fromBuildInfo() = %q, want %q", got, tt.want)
			}
		})
	}
}
