package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDockerfileBase pins how the harness chooses the base image: the tag by
// default, and the digest a qualification run passes in HARNESS_BASE_IMAGE.
func TestDockerfileBase(t *testing.T) {
	for _, tc := range []struct{ env, want string }{
		{"", DefaultBaseImage},
		{"gcr.io/distroless/static@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329", "gcr.io/distroless/static@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329"},
	} {
		t.Setenv("HARNESS_BASE_IMAGE", tc.env)
		path := filepath.Join(t.TempDir(), "Dockerfile")
		if err := writeDockerfile(path, "app"); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if first := strings.SplitN(string(b), "\n", 2)[0]; first != "FROM "+tc.want {
			t.Fatalf("HARNESS_BASE_IMAGE=%q: Dockerfile starts %q, want FROM %s", tc.env, first, tc.want)
		}
	}
}
