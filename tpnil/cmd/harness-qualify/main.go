// Command harness-qualify prepares and verifies the pinned component closure
// that a TPNIL component qualification of this harness runs against, then runs
// the selected scenarios.
//
// The harness compiles each component from a sibling checkout. A qualification
// therefore pins every sibling in tpnil/components.lock.json, and this command
// refuses to run unless each checkout is exactly its pinned commit with no
// tracked modification.
//
//	go run ./tpnil/cmd/harness-qualify prepare
//	go run ./tpnil/cmd/harness-qualify run -run REGEX [-timeout 40m]
//
// prepare runs once before a qualification: it clones each pinned component
// beside this checkout, writes the workspace go.work, and pulls each pinned
// image by digest. run verifies all of that, removes previously built harness
// images so that every image is compiled from the pinned sources, and runs
// `go test` on the scenario package as root, because the harness creates
// network namespaces. Both subcommands run from the root of this checkout.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const lockPath = "tpnil/components.lock.json"

type lockFile struct {
	Schema     string      `json:"schema"`
	Toolchain  string      `json:"toolchain"`
	Components []component `json:"components"`
	Images     []image     `json:"images"`
}

type component struct {
	Name       string `json:"name"`
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Commit     string `json:"commit"`
}

type image struct {
	Reference  string `json:"reference"`
	Repository string `json:"repository"`
	Digest     string `json:"digest"`
}

func main() {
	if len(os.Args) < 2 {
		fail("usage: harness-qualify prepare | run -run REGEX [-timeout 40m]")
	}
	root, err := os.Getwd()
	if err != nil {
		fail("working directory: %v", err)
	}
	lock := readLock(root)
	switch os.Args[1] {
	case "prepare":
		prepare(root, lock)
	case "run":
		flags := flag.NewFlagSet("run", flag.ExitOnError)
		pattern := flags.String("run", "", "go test -run expression selecting the scenarios (required)")
		timeout := flags.String("timeout", "40m", "go test -timeout")
		_ = flags.Parse(os.Args[2:])
		if *pattern == "" {
			fail("run: -run is required")
		}
		os.Exit(run(root, lock, *pattern, *timeout))
	default:
		fail("unknown subcommand %q (want prepare or run)", os.Args[1])
	}
}

func readLock(root string) lockFile {
	raw, err := os.ReadFile(filepath.Join(root, lockPath))
	if err != nil {
		fail("read %s (run from the root of the multicast-test checkout): %v", lockPath, err)
	}
	var l lockFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&l); err != nil {
		fail("decode %s: %v", lockPath, err)
	}
	if l.Schema != "lightweb-multicast-harness-components@1.0.0" || l.Toolchain == "" || len(l.Components) == 0 {
		fail("%s: unexpected schema or empty closure", lockPath)
	}
	return l
}

func prepare(root string, l lockFile) {
	ws := filepath.Dir(root)
	for _, c := range l.Components {
		dir := filepath.Join(ws, c.Name)
		if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
			mustRun("", "git", "-c", "advice.detachedHead=false", "clone", "--quiet", "--depth", "1", "--branch", c.Tag, c.Repository, dir)
		}
		verifyComponent(dir, c)
	}
	want := goWork(root, l)
	path := filepath.Join(ws, "go.work")
	if have, err := os.ReadFile(path); err == nil {
		if !bytes.Equal(have, want) {
			fail("%s exists and differs from the closure; move it aside and prepare again", path)
		}
	} else if err := os.WriteFile(path, want, 0o644); err != nil {
		fail("write %s: %v", path, err)
	}
	fmt.Printf("workspace %s written from %s\n", path, lockPath)
	for _, im := range l.Images {
		pinned := im.Repository + "@" + im.Digest
		mustRun("", "sudo", "-n", "docker", "pull", "--quiet", pinned)
		mustRun("", "sudo", "-n", "docker", "tag", pinned, im.Reference)
		fmt.Printf("image %s pulled as %s\n", pinned, im.Reference)
	}
	fmt.Println("closure prepared")
}

func run(root string, l lockFile, pattern, timeout string) int {
	ws := filepath.Dir(root)
	fmt.Println("TPNIL component qualification of the Lightweb Inc. multicast-test harness")
	for _, c := range l.Components {
		verifyComponent(filepath.Join(ws, c.Name), c)
	}
	fmt.Printf("components verified: %d of %d\n", len(l.Components), len(l.Components))

	work := filepath.Join(ws, "go.work")
	have, err := os.ReadFile(work)
	if err != nil || !bytes.Equal(have, goWork(root, l)) {
		fail("%s is missing or differs from the closure; run prepare", work)
	}
	sum := sha256.Sum256(have)
	fmt.Printf("workspace go.work verified sha256:%s\n", hex.EncodeToString(sum[:]))

	if v := strings.TrimSpace(mustOutput("", "go", "env", "GOVERSION")); v != l.Toolchain {
		fail("toolchain %s does not match the pinned %s", v, l.Toolchain)
	}
	fmt.Printf("toolchain %s verified\n", l.Toolchain)

	for _, im := range l.Images {
		var digests []string
		out := mustOutput("", "sudo", "-n", "docker", "image", "inspect", "--format", "{{json .RepoDigests}}", im.Reference)
		if err := json.Unmarshal([]byte(out), &digests); err != nil {
			fail("image %s: %v", im.Reference, err)
		}
		ok := false
		for _, d := range digests {
			ok = ok || strings.HasSuffix(d, "@"+im.Digest)
		}
		if !ok {
			fail("image %s is not the pinned %s@%s; run prepare", im.Reference, im.Repository, im.Digest)
		}
		fmt.Printf("image %s %s verified\n", im.Reference, im.Digest)
	}

	stale := strings.Fields(mustOutput("", "sudo", "-n", "docker", "images", "--filter", "reference=*:harness", "-q"))
	stale = unique(stale)
	if len(stale) > 0 {
		mustRun("", "sudo", append([]string{"-n", "docker", "image", "rm", "-f"}, stale...)...)
	}
	fmt.Printf("harness images removed before the run: %d\n", len(stale))

	// The scenarios run tc, ip and ip6tables as root. A login PATH on Debian
	// omits the sbin directories, so the root run appends them.
	rootPath := os.Getenv("PATH") + ":/usr/local/sbin:/usr/sbin:/sbin"
	for _, tool := range []string{"tc", "ip", "ip6tables"} {
		if _, err := exec.LookPath(tool); err != nil {
			if out, err := exec.Command("sudo", "-n", "env", "PATH="+rootPath, "sh", "-c", "command -v "+tool).Output(); err != nil || len(out) == 0 {
				fail("host tool %s not found for the root run", tool)
			}
		}
	}
	fmt.Println("host tools tc, ip, ip6tables found")
	fmt.Printf("docker server %s\n", strings.TrimSpace(mustOutput("", "sudo", "-n", "docker", "version", "--format", "{{.Server.Version}}")))

	cmd := exec.Command("sudo", "-n", "env",
		"PATH="+rootPath, "GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly",
		"go", "test", "./harness/scenarios/...", "-v", "-count=1", "-timeout", timeout, "-run", pattern)
	cmd.Dir = root
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			fail("go test: %v", err)
		}
		code = exit.ExitCode()
	}

	built := mustOutput("", "sudo", "-n", "docker", "images", "--filter", "reference=*:harness",
		"--format", "{{.Repository}}:{{.Tag}} {{.ID}}")
	for _, line := range strings.Split(strings.TrimSpace(built), "\n") {
		if line != "" {
			fmt.Printf("harness image %s built from the pinned sources\n", line)
		}
	}
	fmt.Printf("harness qualification exit %d\n", code)
	return code
}

// verifyComponent requires the pinned commit, the pinned origin and no
// tracked modification. Untracked files are allowed: the harness writes each
// compiled binary into its component's checkout.
func verifyComponent(dir string, c component) {
	if head := strings.TrimSpace(mustOutput(dir, "git", "rev-parse", "HEAD")); head != c.Commit {
		fail("component %s at %s, pinned %s (%s)", c.Name, head, c.Commit, c.Tag)
	}
	if origin := strings.TrimSpace(mustOutput(dir, "git", "remote", "get-url", "origin")); origin != c.Repository {
		fail("component %s origin %s, pinned %s", c.Name, origin, c.Repository)
	}
	if dirty := strings.TrimSpace(mustOutput(dir, "git", "status", "--porcelain", "--untracked-files=no")); dirty != "" {
		fail("component %s has tracked modifications:\n%s", c.Name, dirty)
	}
	fmt.Printf("component %s %s %s verified\n", c.Name, c.Tag, c.Commit)
}

// goWork renders the workspace file for the closure: the harness checkout and
// every pinned component, at the highest go version any of them declares.
func goWork(root string, l lockFile) []byte {
	ws := filepath.Dir(root)
	dirs := []string{filepath.Base(root)}
	for _, c := range l.Components {
		dirs = append(dirs, c.Name)
	}
	sort.Strings(dirs)
	goVersion := "1.0"
	for _, d := range dirs {
		if v := goDirective(filepath.Join(ws, d, "go.mod")); newer(v, goVersion) {
			goVersion = v
		}
	}
	var b strings.Builder
	b.WriteString("// Generated by tpnil/cmd/harness-qualify from tpnil/components.lock.json.\n\n")
	fmt.Fprintf(&b, "go %s\n\nuse (\n", goVersion)
	for _, d := range dirs {
		fmt.Fprintf(&b, "\t./%s\n", d)
	}
	b.WriteString(")\n")
	return []byte(b.String())
}

func goDirective(modFile string) string {
	raw, err := os.ReadFile(modFile)
	if err != nil {
		fail("read %s: %v", modFile, err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "go" {
			return f[1]
		}
	}
	fail("%s declares no go version", modFile)
	return ""
}

// newer reports whether dotted version a is greater than b.
func newer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func mustRun(dir, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		fail("%s %s: %v", name, strings.Join(args, " "), err)
	}
}

func mustOutput(dir, name string, args ...string) string {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		fail("%s %s: %v", name, strings.Join(args, " "), err)
	}
	return string(out)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "harness-qualify: "+format+"\n", args...)
	os.Exit(2)
}
