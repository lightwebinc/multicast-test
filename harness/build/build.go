// Package build provides helpers to cross-compile component binaries on the
// host and package them into minimal Docker images for the test harness.
//
// All component repos share a private dependency on
// github.com/lightwebinc/bitcoin-shard-common. Because the module is not
// publicly available on the Go module proxy, the standard "go mod download"
// inside a docker build would fail. Instead, this package:
//
//  1. Injects a temporary "replace" directive pointing to the local checkout.
//  2. Cross-compiles a static Linux/amd64 binary on the host.
//  3. Packages it into a minimal distroless Docker image.
//  4. Removes the replace directive (always, via defer).
package build

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const commonModule = "github.com/lightwebinc/bitcoin-shard-common"

// ImageSpec describes one component image to build.
type ImageSpec struct {
	// RepoDir is the absolute path to the component's git repo.
	RepoDir string
	// MainPkg is the Go import path of the main package to compile, relative to
	// RepoDir (e.g. "." or "cmd/subtx-gen").
	MainPkg string
	// Binary is the output binary filename (no path).
	Binary string
	// Tag is the Docker image tag, e.g. "bitcoin-shard-proxy:harness".
	Tag string
}

// commonDir returns the absolute path of bitcoin-shard-common, which must
// live alongside all component repos under the same parent.
func commonDir(anyRepoDir string) string {
	return filepath.Join(filepath.Dir(anyRepoDir), "bitcoin-shard-common")
}

// BuildAll builds Docker images for all given specs. If a spec's image already
// exists (docker image inspect succeeds) it is skipped unless force is true.
func BuildAll(ctx context.Context, specs []ImageSpec, force bool) error {
	for _, s := range specs {
		if err := Build(ctx, s, force); err != nil {
			return fmt.Errorf("build %s: %w", s.Tag, err)
		}
	}
	return nil
}

// Build compiles the binary on the host and creates a Docker image.
func Build(ctx context.Context, s ImageSpec, force bool) error {
	if !force {
		if imageExists(ctx, s.Tag) {
			fmt.Fprintf(os.Stderr, "[build] %s already exists, skipping\n", s.Tag)
			return nil
		}
	}
	fmt.Fprintf(os.Stderr, "[build] building %s from %s\n", s.Tag, s.RepoDir)

	// 1. Resolve how to provide bitcoin-shard-common inside the build.
	// Prefer the go.work workspace file in the parent directory, which
	// already lists all component repos (including shard-common). If found,
	// pass GOWORK to the compiler so go module replace magic is automatic.
	// Fall back to a temporary go.mod replace directive for environments
	// without the workspace (e.g. CI agents that check out a single repo).
	workFile := filepath.Join(filepath.Dir(s.RepoDir), "go.work")
	var extraEnv []string
	if _, err := os.Stat(workFile); err == nil {
		extraEnv = append(extraEnv, "GOWORK="+workFile)
	} else {
		commonPath := commonDir(s.RepoDir)
		if _, err2 := os.Stat(commonPath); err2 != nil {
			return fmt.Errorf("bitcoin-shard-common not found at %s and no go.work: %w", commonPath, err2)
		}
		if err3 := modReplace(ctx, s.RepoDir, commonPath); err3 != nil {
			return fmt.Errorf("go mod edit -replace: %w", err3)
		}
		defer func() {
			if err4 := modDropReplace(context.Background(), s.RepoDir); err4 != nil {
				fmt.Fprintf(os.Stderr, "[build] WARN dropreplace %s: %v\n", s.RepoDir, err4)
			}
		}()
	}

	// 2. Cross-compile.
	binPath := filepath.Join(s.RepoDir, s.Binary)
	pkg := s.MainPkg
	if pkg == "" {
		pkg = "."
	}
	cmd := exec.CommandContext(ctx, "go", "build",
		"-trimpath", "-buildvcs=false",
		"-o", binPath,
		pkg,
	)
	cmd.Dir = s.RepoDir
	cmd.Env = append(append(os.Environ(), extraEnv...),
		"CGO_ENABLED=0",
		"GOOS=linux",
		"GOARCH=amd64",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go build: %w", err)
	}
	defer os.Remove(binPath) //nolint:errcheck

	// 3. Write a temporary Dockerfile.
	dfPath := filepath.Join(s.RepoDir, ".Dockerfile.harness")
	if err := writeDockerfile(dfPath, s.Binary); err != nil {
		return fmt.Errorf("write Dockerfile: %w", err)
	}
	defer os.Remove(dfPath) //nolint:errcheck

	// 4. docker build.
	buildCmd := exec.CommandContext(ctx, "docker", "build",
		"-f", dfPath,
		"-t", s.Tag,
		s.RepoDir,
	)
	buildCmd.Stdout = os.Stderr
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[build] %s done\n", s.Tag)
	return nil
}

// DefaultSpecs returns the standard set of ImageSpecs assuming all repos live
// under repoRoot (e.g. /home/light/repo).
func DefaultSpecs(repoRoot string) []ImageSpec {
	return []ImageSpec{
		{
			RepoDir: filepath.Join(repoRoot, "bitcoin-shard-proxy"),
			MainPkg: ".",
			Binary:  "bitcoin-shard-proxy",
			Tag:     "bitcoin-shard-proxy:harness",
		},
		{
			RepoDir: filepath.Join(repoRoot, "bitcoin-shard-listener"),
			MainPkg: ".",
			Binary:  "bitcoin-shard-listener",
			Tag:     "bitcoin-shard-listener:harness",
		},
		{
			RepoDir: filepath.Join(repoRoot, "bitcoin-retry-endpoint"),
			MainPkg: ".",
			Binary:  "bitcoin-retry-endpoint",
			Tag:     "bitcoin-retry-endpoint:harness",
		},
		{
			RepoDir: filepath.Join(repoRoot, "bitcoin-subtx-generator"),
			MainPkg: "./cmd/subtx-gen",
			Binary:  "subtx-gen",
			Tag:     "bitcoin-subtx-generator:harness",
		},
	}
}

// imageExists returns true if the given Docker image tag is present locally.
func imageExists(ctx context.Context, tag string) bool {
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", tag)
	out, err := cmd.CombinedOutput()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// modReplace adds a replace directive for bitcoin-shard-common to go.mod.
func modReplace(ctx context.Context, repoDir, commonPath string) error {
	cmd := exec.CommandContext(ctx, "go", "mod", "edit",
		"-replace", commonModule+"="+commonPath,
	)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// modDropReplace removes the replace directive for bitcoin-shard-common.
func modDropReplace(ctx context.Context, repoDir string) error {
	cmd := exec.CommandContext(ctx, "go", "mod", "edit",
		"-dropreplace", commonModule,
	)
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

var dockerfileTmpl = template.Must(template.New("df").Parse(`FROM gcr.io/distroless/static:nonroot
COPY {{.Binary}} /{{.Binary}}
ENTRYPOINT ["/{{.Binary}}"]
`))

func writeDockerfile(path, binary string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return dockerfileTmpl.Execute(f, struct{ Binary string }{binary})
}
