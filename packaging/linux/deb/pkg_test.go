package deb

import (
	"testing"

	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestDebArchFromPlatform(t *testing.T) {
	tests := []struct {
		name     string
		platform ocispecs.Platform
		want     string
		wantErr  bool
	}{
		{name: "amd64", platform: ocispecs.Platform{OS: "linux", Architecture: "amd64"}, want: "amd64"},
		{name: "arm64", platform: ocispecs.Platform{OS: "linux", Architecture: "arm64"}, want: "arm64"},
		{name: "ppc64le", platform: ocispecs.Platform{OS: "linux", Architecture: "ppc64le"}, want: "ppc64el"},
		{name: "arm v7", platform: ocispecs.Platform{OS: "linux", Architecture: "arm", Variant: "v7"}, want: "armhf"},
		{name: "missing variant", platform: ocispecs.Platform{OS: "linux", Architecture: "arm"}, wantErr: true},
		{name: "unsupported arch", platform: ocispecs.Platform{OS: "linux", Architecture: "mips"}, wantErr: true},
		{name: "empty", platform: ocispecs.Platform{}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := debArchFromPlatform(tt.platform)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, got)
			}
		})
	}
}
