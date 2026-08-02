package devconfig

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerStarterIsAnIndependentRepository(t *testing.T) {
	root := filepath.Join("..", "..")
	dir := filepath.Join(root, "templates", "worker")
	for _, relative := range []string{
		"README.md", "Makefile", "go.mod", "go.sum", "Dockerfile", ".dockerignore",
		"types.go", "activities.go", "definition.go", "cmd/worker/main.go",
		"scripts/build-image.sh", "scripts/push-image.sh", "scripts/kind-load.sh",
	} {
		info, err := os.Lstat(filepath.Join(dir, relative))
		if err != nil {
			t.Errorf("missing Worker Starter file %s: %v", relative, err)
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			t.Errorf("%s must not be a symlink", relative)
		}
	}

	module := read(t, filepath.Join(dir, "go.mod"))
	if strings.Contains(module, "replace ") || strings.Contains(module, "../..") || !versionedOrgSDK.MatchString(module) {
		t.Errorf("Starter go.mod must use a versioned public Org SDK without a parent replace:\n%s", module)
	}
	for _, relative := range []string{"types.go", "activities.go", "definition.go", "cmd/worker/main.go"} {
		contents := read(t, filepath.Join(dir, relative))
		for _, forbidden := range []string{"go.temporal.io/", "HelloWorkflow", "GreetingInput", "demo delay"} {
			if strings.Contains(contents, forbidden) {
				t.Errorf("%s contains Sample or platform detail %q", relative, forbidden)
			}
		}
	}
	makefile := read(t, filepath.Join(dir, "Makefile"))
	for _, target := range []string{"test:", "vet:", "verify:", "image:", "push:", "kind-load:"} {
		if !strings.Contains(makefile, target) {
			t.Errorf("Starter Makefile missing %q", target)
		}
	}
	build := read(t, filepath.Join(dir, "scripts", "build-image.sh"))
	for _, want := range []string{"worker_dir=", "docker build", `"$worker_dir"`, "--kind-load", "--push"} {
		if !strings.Contains(build, want) {
			t.Errorf("Starter build script missing %q", want)
		}
	}
	for relative, want := range map[string]string{
		"scripts/kind-load.sh":  "IMAGE_DIGEST=",
		"scripts/push-image.sh": "IMAGE_DIGEST=",
	} {
		if !strings.Contains(read(t, filepath.Join(dir, relative)), want) {
			t.Errorf("%s missing %q", relative, want)
		}
	}
}

func TestCopiedWorkerStarterVerifiesWithoutOrgParent(t *testing.T) {
	root := filepath.Join("..", "..")
	destination := filepath.Join(t.TempDir(), "my-worker")
	if err := os.CopyFS(destination, os.DirFS(filepath.Join(root, "templates", "worker"))); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("make", "verify")
	command.Dir = destination
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("copied Worker Starter verify: %v\n%s", err, output)
	}
}

func TestUserDocsSeparateStarterTutorialSDKReferenceAndSamples(t *testing.T) {
	root := filepath.Join("..", "..")
	tutorial := read(t, filepath.Join(root, "docs", "user", "create-your-worker.md"))
	for _, want := range []string{
		"templates/worker", "go mod edit -module", "WorkflowName", "Activity ID", "Worker `my-worker`",
		"make verify", "make kind-load", "IMAGE_DIGEST", "发布版本", "启动 Workflow", "Run detail",
	} {
		if !strings.Contains(tutorial, want) {
			t.Errorf("create-your-worker.md missing executable path %q", want)
		}
	}

	reference := read(t, filepath.Join(root, "docs", "user", "write-your-worker.md"))
	for _, want := range []string{"# Org SDK 开发参考", "Definition", "Workflow", "Activity", "ActivityPolicy", "NewTestEnvironment", "RunHostedWorker"} {
		if !strings.Contains(reference, want) {
			t.Errorf("write-your-worker.md missing SDK reference %q", want)
		}
	}
	for _, forbidden := range []string{"编写你的第一个", "先复制", "用 CI 或 Sample 的 Makefile"} {
		if strings.Contains(reference, forbidden) {
			t.Errorf("write-your-worker.md still acts as a scaffold tutorial via %q", forbidden)
		}
	}

	for _, relative := range []string{"docs/README.md", "docs/user/README.md"} {
		contents := read(t, filepath.Join(root, relative))
		if !strings.Contains(contents, "create-your-worker.md") || !strings.Contains(contents, "write-your-worker.md") {
			t.Errorf("%s must link both the tutorial and SDK reference", relative)
		}
	}

	for _, relative := range []string{"samples/README.md", "samples/hello/README.md", "samples/parallel-confirmation/README.md", "samples/dynamic-decision/README.md"} {
		contents := read(t, filepath.Join(root, relative))
		for _, forbidden := range []string{"复制这个 Sample", "复制本 Sample", "作为项目模板"} {
			if strings.Contains(contents, forbidden) {
				t.Errorf("%s asks users to treat a Sample as a scaffold via %q", relative, forbidden)
			}
		}
	}

	gettingStarted := read(t, filepath.Join(root, "docs", "user", "getting-started.md"))
	for _, forbidden := range []string{
		"make test", "make vet", "ORG_REGISTRY_ALLOWLIST=", "Temporal Web",
		"管理 Tenants", "Version 一直等待 registration", "bootstrap token", "Temporal Event History",
	} {
		if strings.Contains(gettingStarted, forbidden) {
			t.Errorf("getting-started.md retains nonessential quick-run detail %q", forbidden)
		}
	}
}

func TestQuickRunDocsOnlyAskForVisibleUserInput(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, relative := range []string{"docs/user/getting-started.md", "docs/user/create-your-worker.md"} {
		contents := read(t, filepath.Join(root, relative))
		for _, forbidden := range []string{
			"`100m` CPU", "`128Mi` memory", "candidate deployment",
			"SDK automatic registration", "pinned contract probe", "poller",
		} {
			if strings.Contains(contents, forbidden) {
				t.Errorf("%s exposes a default or internal publish detail %q", relative, forbidden)
			}
		}
	}

	tutorial := read(t, filepath.Join(root, "docs", "user", "create-your-worker.md"))
	for _, want := range []string{"## 4. 发布版本", "## 5. 启动 Workflow", "## 6. 观察结果", "> 在 Console", "> 选择 `MyWorkflow`", "> 打开 Run detail"} {
		if !strings.Contains(tutorial, want) {
			t.Errorf("create-your-worker.md missing lightweight UI section %q", want)
		}
	}
}
