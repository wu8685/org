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
			for _, relative := range []string{"Makefile", "README.md", "go.mod", "go.sum", "Dockerfile", "scripts/build-image.sh", "scripts/push-image.sh", "scripts/kind-load.sh"} {
				info, err := os.Lstat(filepath.Join(dir, relative))
				if err != nil {
					t.Errorf("missing independent repository file %s: %v", relative, err)
					continue
				}
				if info.Mode()&os.ModeSymlink != 0 {
					t.Errorf("%s must not be a symlink", relative)
				}
			}
			for _, relative := range []string{"cmd/generate-manifest", "generated", "config", "config.go", "config_test.go"} {
				if _, err := os.Lstat(filepath.Join(dir, relative)); !os.IsNotExist(err) {
					t.Errorf("slim Sample retains obsolete path %s", relative)
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
			for _, want := range []string{"make test", "make push", "make kind-load", "IMAGE_DIGEST", "自动注册", "平台注入", "SOURCE_REVISION=abcdef1", `COMMIT="$SOURCE_REVISION"`} {
				if !strings.Contains(readme, want) {
					t.Errorf("README missing %q", want)
				}
			}
			for _, forbidden := range []string{"../../docs", "make sample-test", "make parallel-sample", "make dynamic-sample", "manifest", "Task Queue", "Build ID", "TEMPORAL_", "config/release.example.json", "git rev-parse"} {
				if strings.Contains(readme, forbidden) {
					t.Errorf("README retains repository/platform dependency %q", forbidden)
				}
			}
		})
	}
}

func TestCentralPublishExampleMatchesDigestOnlyContract(t *testing.T) {
	root := filepath.Join("..", "..")
	contents := read(t, filepath.Join(root, "docs", "api", "examples", "publish-worker-version.json"))
	var request domain.WorkerVersionPublishRequest
	if err := json.Unmarshal([]byte(contents), &request); err != nil {
		t.Fatal(err)
	}
	request.WorkerName = "hello-worker"
	if err := domain.ValidateWorkerVersionPublish(request, []string{"registry.example.com"}); err != nil {
		t.Fatalf("release example does not match publish contract: %v", err)
	}
	for _, forbidden := range []string{"tenantId", "tenantSlug", "scope", "workerName", "manifest", "metadata", "contract", "bootstrap", "taskQueue", "temporalNamespace", "kubernetesNamespace"} {
		if strings.Contains(contents, `"`+forbidden+`"`) {
			t.Errorf("release example contains server-owned field %q", forbidden)
		}
	}
}

func TestCopiedSamplesVerifyWithoutOrgParent(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, sample := range []string{"hello", "parallel-confirmation", "dynamic-decision"} {
		t.Run(sample, func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), sample)
			if err := os.CopyFS(destination, os.DirFS(filepath.Join(root, "samples", sample))); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("make", "verify")
			command.Dir = destination
			command.Env = append(os.Environ(), "GOWORK=off")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("copied Sample verify: %v\n%s", err, output)
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

func TestConsoleDevDefaultsToLocalKindRegistriesWithoutChangingProductionDefaults(t *testing.T) {
	root := filepath.Join("..", "..")
	makefile := read(t, filepath.Join(root, "Makefile"))
	if !strings.Contains(makefile, `ORG_REGISTRY_ALLOWLIST="$${ORG_REGISTRY_ALLOWLIST:-org.local,ghcr.io}" go run ./cmd/org-console`) {
		t.Fatal("console-dev must default to org.local,ghcr.io while preserving an explicit environment override")
	}
	config := read(t, filepath.Join(root, "internal", "config", "config.go"))
	if strings.Contains(config, `RegistryAllowlist: []string{"org.local", "ghcr.io"}`) {
		t.Fatal("production config default must not implicitly trust the local org.local registry")
	}
	request := domain.WorkerVersionPublishRequest{
		WorkerName: "hello-worker", Version: "2026.08.2", Description: "Local Hello release",
		Image:   "org.local/hello-worker@sha256:" + strings.Repeat("a", 64),
		Runtime: domain.RuntimeSpec{CPU: "100m", Memory: "128Mi"},
	}
	if err := domain.ValidateWorkerVersionPublish(request, []string{"org.local", "ghcr.io"}); err != nil {
		t.Fatalf("local Hello immutable digest was rejected by the console-dev allowlist: %v", err)
	}
	gettingStarted := read(t, filepath.Join(root, "docs", "getting-started.md"))
	if !strings.Contains(gettingStarted, "make console-dev") || !strings.Contains(gettingStarted, "org.local,ghcr.io") || !strings.Contains(gettingStarted, "local-dev default") {
		t.Fatal("Getting Started must explain the local console registry default and explicit command")
	}
}

func TestUserDocumentationHasACompleteValueFirstPath(t *testing.T) {
	root := filepath.Join("..", "..")
	files := map[string][]string{
		"README.md":                          {"Agent", "写 code", "自定义流程", "动态加载和注册", "Tenant", "Worker", "Version", "Workflow", "Run", "immutable", "Org SDK", "Console", "docs/README.md", "docs/getting-started.md", "docs/concepts.md", "samples/README.md"},
		"docs/README.md":                     {"第一次使用", "开发 Worker", "维护 org", "getting-started.md", "concepts.md", "api/publish-worker-version.md", "architecture/overview.md", "development.md", "specs/"},
		"docs/concepts.md":                   {"Tenant", "Worker", "Version", "Workflow", "Run", "一次发布", "一次运行", "用户负责", "org 负责"},
		"docs/getting-started.md":            {"完成后你会得到", "检查点", "kind-org", "127.0.0.1:7233", "make console-dev", "cd samples/hello", "make kind-load", "IMAGE_DIGEST", "Run", "YAML", "HTTP/JSON API", "api/publish-worker-version.md"},
		"docs/api/publish-worker-version.md": {"GET /api/v1/session", "X-CSRF-Token", "Idempotency-Key", "POST /api/v1/workers/{workerName}/versions", "immutable", "description", "image", "runtime", "server-derived"},
		"docs/architecture/overview.md":      {"Org SDK", "control plane", "Worker", "Temporal", "Kubernetes", "semantic projection", "dynamic DAG", "Gateway"},
		"samples/README.md":                  {"hello", "parallel-confirmation", "dynamic-decision", "make test", "make kind-load", "skipped", "waiting-for-user"},
	}
	for relative, wants := range files {
		text := read(t, filepath.Join(root, relative))
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing %q", relative, want)
			}
		}
	}
	for _, relative := range []string{"docs/api/publish-worker-version.md", "docs/api/examples/publish-worker-version.json", "docs/getting-started.md", "samples/hello/README.md", "samples/parallel-confirmation/README.md", "samples/dynamic-decision/README.md"} {
		text := read(t, filepath.Join(root, relative))
		for _, forbidden := range []string{"source provenance through org", "source provenance。", `"source":`, "source 填写"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s still asks the user for publish provenance %q", relative, forbidden)
			}
		}
	}
	sampleOverview := read(t, filepath.Join(root, "samples", "README.md"))
	if strings.Contains(sampleOverview, "git rev-parse") || !strings.Contains(sampleOverview, "SOURCE_REVISION=abcdef1") || !strings.Contains(sampleOverview, `COMMIT="$SOURCE_REVISION"`) {
		t.Errorf("samples/README.md must support copied repositories without Git metadata")
	}
	parallel := read(t, filepath.Join(root, "samples", "parallel-confirmation", "README.md"))
	if !strings.Contains(parallel, `{"subject":"release notes"}`) {
		t.Errorf("parallel-confirmation README must include required Workflow input")
	}
	for _, relative := range []string{"docs/getting-started.md", "samples/hello/README.md", "samples/parallel-confirmation/README.md", "samples/dynamic-decision/README.md"} {
		text := read(t, filepath.Join(root, relative))
		for _, want := range []string{"YAML", "JSON", "Run description", "可选", "schema"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing structured Trigger guidance %q", relative, want)
			}
		}
		if strings.Contains(text, "Console 自动生成") || strings.Contains(text, "标准输入字段") {
			t.Errorf("%s still implies schema-derived Trigger form controls", relative)
		}
	}
	for _, relative := range []string{"samples/parallel-confirmation/README.md", "samples/dynamic-decision/README.md"} {
		text := read(t, filepath.Join(root, relative))
		for _, want := range []string{"100m", "128Mi", "production", "Activity", "2–5 秒", "随机", "running", "仅用于教学演示"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing resource guidance %q", relative, want)
			}
		}
	}
}

func TestLocalDemoResetIsDiscoverableAndKeepsFixedOwnershipBoundaries(t *testing.T) {
	root := filepath.Join("..", "..")
	makefile := read(t, filepath.Join(root, "Makefile"))
	for _, want := range []string{"demo-reset:", "demo-reset-dry-run:", "sh scripts/demo-reset.sh", "sh scripts/demo-reset_test.sh"} {
		if !strings.Contains(makefile, want) {
			t.Errorf("Makefile missing local demo reset entry %q", want)
		}
	}
	for _, relative := range []string{"README.md", "docs/getting-started.md"} {
		contents := read(t, filepath.Join(root, relative))
		for _, want := range []string{"make demo-reset-dry-run", "RESET_DEMO=1 make demo-reset", "停止", "备份"} {
			if !strings.Contains(contents, want) {
				t.Errorf("%s missing reset guidance %q", relative, want)
			}
		}
	}
	script := read(t, filepath.Join(root, "scripts", "demo-reset.sh"))
	for _, forbidden := range []string{"kind delete", "temporal workflow delete", "docker image rm", "crictl rmi", "org-e2e-", "${NAMESPACE", "${KUBE_CONTEXT"} {
		if strings.Contains(script, forbidden) {
			t.Errorf("demo reset crosses fixed local ownership via %q", forbidden)
		}
	}
}

func TestApprovedDocsDoNotRetainRemovedSampleArtifactPaths(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, relative := range []string{
		"README.md", "docs/getting-started.md", "samples/README.md",
		"docs/specs/006-org-sdk.md", "docs/specs/007-hello-org-sdk-sample.md",
		"docs/specs/008-parallel-confirmation-org-sdk-sample.md", "docs/specs/009-dynamic-decision-org-sdk-sample.md",
		"docs/specs/012-worker-bootstrap-registration.md", "docs/specs/013-sample-repository-independence.md",
		"samples/hello/README.md", "samples/parallel-confirmation/README.md", "samples/dynamic-decision/README.md",
	} {
		contents := read(t, filepath.Join(root, relative))
		for _, forbidden := range []string{"cmd/generate-manifest", "generated/org-worker-manifest.json", "config/release.example.json"} {
			if strings.Contains(contents, forbidden) {
				t.Errorf("%s retains removed Sample path %q", relative, forbidden)
			}
		}
	}
}

func TestUserDocumentationLocalLinksResolve(t *testing.T) {
	root := filepath.Join("..", "..")
	linkPattern := regexp.MustCompile(`\[[^]]+\]\(([^)]+)\)`)
	for _, relative := range []string{"README.md", "docs/getting-started.md", "docs/api/publish-worker-version.md", "docs/architecture/overview.md", "samples/README.md"} {
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
