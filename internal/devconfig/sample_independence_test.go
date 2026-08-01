package devconfig

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/wu8685/org/internal/domain"
)

var versionedOrgSDK = regexp.MustCompile(`(?m)^(?:require\s+)?\s*github\.com/wu8685/org\s+v[0-9]`)

func TestSamplesAreSelfContainedVersionedWorkerRepositories(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, sample := range []string{"hello", "parallel-confirmation", "dynamic-decision"} {
		t.Run(sample, func(t *testing.T) {
			dir := filepath.Join(root, "samples", sample)
			for _, relative := range []string{"Makefile", "README.md", "go.mod", "go.sum", "Dockerfile", "scripts/build-image.sh", "scripts/push-image.sh", "scripts/kind-load.sh", "config/release.example.json"} {
				info, err := os.Lstat(filepath.Join(dir, relative))
				if err != nil {
					t.Errorf("missing independent repository file %s: %v", relative, err)
					continue
				}
				if info.Mode()&os.ModeSymlink != 0 {
					t.Errorf("%s must not be a symlink", relative)
				}
			}

			module := read(t, filepath.Join(dir, "go.mod"))
			if strings.Contains(module, "replace ") || strings.Contains(module, "../..") || !versionedOrgSDK.MatchString(module) {
				t.Errorf("go.mod must use a versioned public Org SDK without a parent replace:\n%s", module)
			}
			dockerfile := read(t, filepath.Join(dir, "Dockerfile"))
			for _, forbidden := range []string{"COPY sdk ", "COPY samples/", "../.."} {
				if strings.Contains(dockerfile, forbidden) {
					t.Errorf("Dockerfile depends on org root via %q", forbidden)
				}
			}
			build := read(t, filepath.Join(dir, "scripts", "build-image.sh"))
			for _, forbidden := range []string{"repo_root=", "$sample_dir/../..", `"$repo_root"`} {
				if strings.Contains(build, forbidden) {
					t.Errorf("build script depends on org root via %q", forbidden)
				}
			}
			makefile := read(t, filepath.Join(dir, "Makefile"))
			for _, target := range []string{"test:", "vet:", "verify:", "image:", "push:", "kind-load:"} {
				if !strings.Contains(makefile, target) {
					t.Errorf("Makefile missing %q", target)
				}
			}
			readme := read(t, filepath.Join(dir, "README.md"))
			for _, want := range []string{"make test", "make push", "make kind-load", "IMAGE_DIGEST", "自动注册", "平台注入"} {
				if !strings.Contains(readme, want) {
					t.Errorf("README missing %q", want)
				}
			}
			for _, forbidden := range []string{"../../docs", "make sample-test", "make parallel-sample", "make dynamic-sample", "manifest upload", "上传 manifest"} {
				if strings.Contains(readme, forbidden) {
					t.Errorf("README retains repository/platform dependency %q", forbidden)
				}
			}
		})
	}
}

func TestSampleReleaseExamplesMatchDigestOnlyPublishContract(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, sample := range []struct{ directory, worker string }{
		{"hello", "hello-worker"},
		{"parallel-confirmation", "parallel-confirmation-worker"},
		{"dynamic-decision", "dynamic-decision-worker"},
	} {
		t.Run(sample.directory, func(t *testing.T) {
			contents := read(t, filepath.Join(root, "samples", sample.directory, "config", "release.example.json"))
			var request domain.WorkerVersionPublishRequest
			if err := json.Unmarshal([]byte(contents), &request); err != nil {
				t.Fatal(err)
			}
			request.WorkerName = sample.worker
			if err := domain.ValidateWorkerVersionPublish(request, []string{"registry.example.com"}); err != nil {
				t.Fatalf("release example does not match publish contract: %v", err)
			}
			for _, forbidden := range []string{"tenantId", "tenantSlug", "scope", "workerName", "manifest", "metadata", "contract", "bootstrap", "taskQueue", "temporalNamespace", "kubernetesNamespace"} {
				if strings.Contains(contents, `"`+forbidden+`"`) {
					t.Errorf("release example contains server-owned field %q", forbidden)
				}
			}
		})
	}
}

func TestCopiedSamplesResolveAndTestWithoutOrgParent(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, sample := range []string{"hello", "parallel-confirmation", "dynamic-decision"} {
		t.Run(sample, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), sample)
			if err := os.CopyFS(destination, os.DirFS(filepath.Join(root, "samples", sample))); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(destination, "generated")); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("make", "test")
			command.Dir = destination
			command.Env = append(os.Environ(), "GOWORK=off")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("copied Sample test: %v\n%s", err, output)
			}
		})
	}
}

func TestSampleImageAndPushTargetsUseLocalContextAndReturnRegistryDigest(t *testing.T) {
	root := filepath.Join("..", "..")
	digest := "sha256:" + strings.Repeat("a", 64)
	for _, sample := range []string{"hello", "parallel-confirmation", "dynamic-decision"} {
		t.Run(sample, func(t *testing.T) {
			fakeBin := t.TempDir()
			calls := filepath.Join(t.TempDir(), "docker-calls")
			fakeDocker := filepath.Join(fakeBin, "docker")
			script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$DOCKER_CALLS"
case "${1:-}" in
  info|build) exit 0 ;;
  push) printf '%s\n' 'layer: pushed' 'digest: ` + digest + ` size: 1234' ;;
  *) echo "unexpected docker command: $*" >&2; exit 2 ;;
esac
`
			if err := os.WriteFile(fakeDocker, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			dir, err := filepath.Abs(filepath.Join(root, "samples", sample))
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command("make", "push", "VERSION=v1", "COMMIT=abcdef1", "IMAGE_REPOSITORY=registry.example.com/team/worker")
			command.Dir = dir
			command.Env = append(os.Environ(), "PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"), "DOCKER_CALLS="+calls)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("push target: %v\n%s", err, output)
			}
			if !strings.Contains(string(output), "IMAGE_DIGEST=registry.example.com/team/worker@"+digest) {
				t.Fatalf("push output did not expose immutable digest:\n%s", output)
			}
			invocations, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(invocations), dir) || strings.Contains(string(invocations), dir+string(os.PathSeparator)+"..") {
				t.Fatalf("Docker did not use the Sample root as context:\n%s", invocations)
			}
		})
	}
}

func TestRootSampleTargetsOnlyDelegateToSampleMakefiles(t *testing.T) {
	makefile := read(t, filepath.Join("..", "..", "Makefile"))
	for _, delegation := range []string{
		"$(MAKE) -C samples/hello",
		"$(MAKE) -C samples/parallel-confirmation",
		"$(MAKE) -C samples/dynamic-decision",
	} {
		if !strings.Contains(makefile, delegation) {
			t.Errorf("root Makefile missing delegation %q", delegation)
		}
	}
	for _, forbidden := range []string{
		"sh samples/hello/scripts/build-image.sh",
		"sh samples/parallel-confirmation/scripts/build-image.sh",
		"sh samples/dynamic-decision/scripts/build-image.sh",
	} {
		if strings.Contains(makefile, forbidden) {
			t.Errorf("root Makefile owns Sample build logic %q", forbidden)
		}
	}
}

func TestUserDocumentationHasACompleteValueFirstPath(t *testing.T) {
	root := filepath.Join("..", "..")
	files := map[string][]string{
		"README.md":                     {"Tenant", "Worker", "Version", "Workflow", "Run", "immutable", "Org SDK", "Console", "docs/getting-started.md", "docs/architecture/overview.md", "samples/README.md"},
		"docs/getting-started.md":       {"kind-org", "127.0.0.1:7233", "make console-dev", "cd samples/hello", "make kind-load", "IMAGE_DIGEST", "Run"},
		"docs/architecture/overview.md": {"Org SDK", "control plane", "Worker", "Temporal", "Kubernetes", "semantic projection", "dynamic DAG", "Gateway"},
		"samples/README.md":             {"hello", "parallel-confirmation", "dynamic-decision", "make test", "make kind-load", "skipped", "waiting-for-user"},
	}
	for relative, wants := range files {
		text := read(t, filepath.Join(root, relative))
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing %q", relative, want)
			}
		}
	}
}

func TestUserDocumentationLocalLinksResolve(t *testing.T) {
	root := filepath.Join("..", "..")
	linkPattern := regexp.MustCompile(`\[[^]]+\]\(([^)]+)\)`)
	for _, relative := range []string{"README.md", "docs/getting-started.md", "docs/architecture/overview.md", "samples/README.md"} {
		text := read(t, filepath.Join(root, relative))
		for _, match := range linkPattern.FindAllStringSubmatch(text, -1) {
			target := strings.TrimSpace(match[1])
			if target == "" || strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "#") {
				continue
			}
			if fragment := strings.IndexByte(target, '#'); fragment >= 0 {
				target = target[:fragment]
			}
			path := filepath.Clean(filepath.Join(root, filepath.Dir(relative), filepath.FromSlash(target)))
			if _, err := os.Stat(path); err != nil {
				t.Errorf("%s has unresolved local link %q: %v", relative, match[1], err)
			}
		}
	}
}
