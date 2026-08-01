package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/platform/kube"
	temporalplatform "github.com/wu8685/org/internal/platform/temporal"
	"github.com/wu8685/org/internal/service"
	"github.com/wu8685/org/sdk/orgsdk"
)

var digestReferencePattern = regexp.MustCompile(`org\.local/hello-worker@sha256:[0-9a-f]{64}`)
var tagPattern = regexp.MustCompile(`org\.local/hello-worker:[A-Za-z0-9_.-]+`)

type sampleImage struct{ version, tag, digestReference, commit string }
type trackedInvocation struct {
	auth service.AuthenticatedContext
	id   string
}
type acceptanceRun struct {
	t                                   *testing.T
	ctx                                 context.Context
	root, id, workerName, kubeNamespace string
	temporal                            *temporalplatform.Client
	control                             *service.ControlPlane
	store                               *service.MemoryStore
	tenants                             map[string]domain.Tenant
	images                              []sampleImage
	invocations                         []trackedInvocation
	cleanupOnce                         sync.Once
	bootstrapServer                     *http.Server
	bootstrapURL                        string
}

func newAcceptanceRun(t *testing.T, ctx context.Context) *acceptanceRun {
	t.Helper()
	root := repositoryRoot(t)
	requireEnvironment(t, ctx, root)
	id := fmt.Sprintf("%x", time.Now().UnixNano())
	if len(id) > 10 {
		id = id[len(id)-10:]
	}
	run := &acceptanceRun{t: t, ctx: ctx, root: root, id: id, workerName: "hello-worker", kubeNamespace: "org-e2e-" + id}
	t.Cleanup(run.cleanup)
	t.Logf("E2E RUN_ID=%s", id)

	run.command("kubectl", "--context", "kind-org", "create", "namespace", run.kubeNamespace)
	return run
}

func (r *acceptanceRun) buildAndLoadSample(suffix string) sampleImage {
	r.t.Helper()
	version := "e2e-" + r.id + "-" + suffix
	commit := strings.Repeat(map[string]string{"a": "a", "b": "b"}[suffix], 12)
	cmd := exec.CommandContext(r.ctx, "make", "-C", filepath.Join(r.root, "samples", "hello"), "kind-load", "VERSION="+version, "COMMIT="+commit)
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("build/load sample %s: %v\n%s", suffix, err, output)
	}
	digest := digestReferencePattern.FindString(string(output))
	tag := tagPattern.FindString(string(output))
	if digest == "" || tag == "" {
		r.t.Fatalf("Hello Sample kind-load did not print tag and digest reference:\n%s", output)
	}
	image := sampleImage{version: version, tag: tag, digestReference: digest, commit: commit}
	r.images = append(r.images, image)
	r.t.Logf("loaded sample version=%s image=%s", version, digest)
	return image
}

func (r *acceptanceRun) controlPlane() *service.ControlPlane {
	r.t.Helper()
	if r.control != nil {
		return r.control
	}
	executor, err := temporalplatform.Dial(temporalplatform.Config{Address: "127.0.0.1:7233", Namespace: "default", PollTimeout: 2 * time.Minute, PollInterval: 250 * time.Millisecond})
	if err != nil {
		r.t.Fatalf("dial Temporal: %v", err)
	}
	r.temporal = executor
	cluster := kube.New(kube.Config{Namespace: r.kubeNamespace, Context: "kind-org", WorkerTemporalAddress: "host.docker.internal:7233", TemporalNamespace: "default", ReadinessTimeout: 2 * time.Minute}, nil)
	r.store = service.NewMemoryStore()
	r.tenants = map[string]domain.Tenant{"a": r.tenant("a"), "b": r.tenant("b")}
	for _, tenant := range r.tenants {
		if err := r.store.SaveTenant(tenant); err != nil {
			r.t.Fatalf("save E2E tenant: %v", err)
		}
	}
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		r.t.Fatalf("listen bootstrap endpoint: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	r.bootstrapURL = fmt.Sprintf("http://127.0.0.1:%d/internal/v1/bootstrap/register", port)
	r.control = service.New(service.Config{RegistryAllowlist: []string{"org.local"}, BootstrapEndpoint: fmt.Sprintf("http://host.docker.internal:%d/internal/v1/bootstrap/register", port), BootstrapVerifier: service.StrictBootstrapWorkloadVerifier{}}, r.store, cluster, executor)
	if err := r.control.StartBootstrapPromotionController(r.ctx); err != nil {
		r.t.Fatalf("start bootstrap promotion controller: %v", err)
	}
	if err := r.control.StartInvocationReconciler(r.ctx); err != nil {
		r.t.Fatalf("start invocation reconciler: %v", err)
	}
	r.bootstrapServer = &http.Server{Handler: service.NewBootstrapRegistrationHandler(r.control, kube.NewBootstrapEvidenceResolver(kube.Config{Namespace: r.kubeNamespace, Context: "kind-org"}, nil)), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = r.bootstrapServer.Serve(listener) }()
	for suffix := range r.tenants {
		if _, err := r.control.CreateWorker(r.ctx, r.auth(suffix), service.CreateWorkerRequest{WorkerName: r.workerName}); err != nil {
			r.t.Fatalf("create E2E Worker: %v", err)
		}
	}
	return r.control
}

func (r *acceptanceRun) tenant(suffix string) domain.Tenant {
	now := time.Now().UTC()
	return domain.Tenant{
		ID: "tenant-e2e-" + r.id + "-" + suffix, Slug: "e2e-" + r.id + "-" + suffix,
		DisplayName: "E2E Tenant " + strings.ToUpper(suffix), Status: domain.TenantActive,
		QuotaPolicy: domain.TenantQuotaPolicy{MaxReservedCPU: "2", MaxReservedMemory: "2Gi", MaxActiveWorkerPods: 4, MaxActiveReleases: 4, MaxConcurrentRuns: 4, MaxConcurrentDeployments: 1},
		CreatedAt:   now, UpdatedAt: now,
	}
}

func (r *acceptanceRun) auth(suffix string) service.AuthenticatedContext {
	tenant := r.tenants[suffix]
	return service.AuthenticatedContext{
		PrincipalID: "e2e-operator-" + suffix, TenantID: tenant.ID, TenantSlug: tenant.Slug,
		AuthenticationMethod: "e2e", RequestID: "e2e-" + r.id,
		Permissions: map[string]bool{
			service.PermissionWorkerCreate: true, service.PermissionWorkerRead: true, service.PermissionWorkerDeploy: true, service.PermissionWorkerVersionUpdate: true, service.PermissionRunStart: true, service.PermissionRunRead: true,
			service.PermissionRunSignal: true, service.PermissionRunQuery: true, service.PermissionRunCancel: true,
		},
	}
}

func (r *acceptanceRun) workerVersionRequest(image sampleImage) domain.WorkerVersionRequest {
	r.t.Helper()
	return domain.WorkerVersionRequest{
		WorkerName: r.workerName, Description: "Hello Worker release " + image.version + ".", Image: image.digestReference, Version: image.version,
		Runtime: domain.RuntimeSpec{CPU: "100m", Memory: "128Mi", ServiceAccount: "hello-worker"},
		Source:  domain.SourceProvenance{Repository: "https://local.test/org/samples/hello", Branch: "e2e", Commit: image.commit, CIReference: "local-e2e-" + r.id},
	}
}

func (r *acceptanceRun) trackInvocation(auth service.AuthenticatedContext, id string) {
	r.invocations = append(r.invocations, trackedInvocation{auth: auth, id: id})
}

func (r *acceptanceRun) waitForReadyVersion(auth service.AuthenticatedContext, workerName, version string) domain.WorkerVersion {
	r.t.Helper()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		candidate, ok := r.store.WorkerVersion(auth.TenantID, workerName, version)
		if ok && candidate.State == domain.WorkerVersionReady {
			return candidate
		}
		if ok && candidate.State == domain.WorkerVersionFailed {
			r.t.Fatalf("WorkerVersion failed: %s", candidate.Failure)
		}
		select {
		case <-r.ctx.Done():
			r.t.Fatalf("wait for WorkerVersion ready: %v", r.ctx.Err())
		case <-ticker.C:
		}
	}
}

func (r *acceptanceRun) cleanup() {
	r.cleanupOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if r.bootstrapServer != nil {
			_ = r.bootstrapServer.Shutdown(ctx)
		}
		if r.control != nil {
			for _, invocation := range r.invocations {
				_ = r.control.Cancel(ctx, invocation.auth, invocation.id)
			}
		}
		if r.kubeNamespace != "" {
			cmd := exec.CommandContext(ctx, "kubectl", "--context", "kind-org", "delete", "namespace", r.kubeNamespace, "--ignore-not-found=true", "--wait=true", "--timeout=90s")
			if output, err := cmd.CombinedOutput(); err != nil {
				r.t.Errorf("cleanup namespace: %v: %s", err, output)
			}
		}
		for _, image := range r.images {
			for _, target := range []string{image.digestReference, image.tag} {
				inspect := exec.CommandContext(ctx, "docker", "exec", "org-control-plane", "crictl", "inspecti", target)
				if err := inspect.Run(); err != nil {
					continue
				}
				remove := exec.CommandContext(ctx, "docker", "exec", "org-control-plane", "crictl", "rmi", target)
				if output, err := remove.CombinedOutput(); err != nil {
					r.t.Errorf("cleanup kind image %s: %v: %s", target, err, output)
				}
			}
			cmd := exec.CommandContext(ctx, "docker", "image", "rm", image.tag)
			if output, err := cmd.CombinedOutput(); err != nil && !strings.Contains(string(output), "No such image") {
				r.t.Errorf("cleanup Docker image %s: %v: %s", image.tag, err, output)
			}
		}
		if r.temporal != nil {
			r.temporal.Close()
		}
	})
}

func waitForCompletedProjection(t *testing.T, ctx context.Context, control *service.ControlPlane, auth service.AuthenticatedContext, id string) service.InvocationView {
	t.Helper()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		view, err := control.GetInvocation(ctx, auth, id)
		if err == nil {
			if view.Execution.Status == "completed" {
				if view.SemanticProjection == nil || view.SemanticProjection.Status != "completed" || len(view.SemanticProjection.AllowedActions) != 0 || view.Projection.Status != "" {
					t.Fatalf("invalid dynamic semantic projection: dynamic=%#v legacy=%#v", view.SemanticProjection, view.Projection)
				}
				statuses := map[string]orgsdk.NodeStatus{}
				for _, node := range view.SemanticProjection.Nodes {
					statuses[node.TemplateID] = node.Status
				}
				if len(statuses) != 3 || statuses["prepare-greeting"] != orgsdk.NodeStatusCompleted || statuses["compose-greeting"] != orgsdk.NodeStatusCompleted || statuses["completed"] != orgsdk.NodeStatusCompleted {
					t.Fatalf("node projection = %#v", view.SemanticProjection.Nodes)
				}
				return view
			}
			lastErr = fmt.Errorf("execution status %q", view.Execution.Status)
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for invocation %s: %v (last: %v)", id, ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func waitForComposeGreetingRunning(t *testing.T, ctx context.Context, control *service.ControlPlane, auth service.AuthenticatedContext, id string) {
	t.Helper()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		view, err := control.GetInvocation(ctx, auth, id)
		if err == nil && view.SemanticProjection != nil {
			statuses := projectionStatuses(view.SemanticProjection.Nodes)
			if statuses["prepare-greeting"] == orgsdk.NodeStatusCompleted && statuses["compose-greeting"] == orgsdk.NodeStatusRunning {
				return
			}
			if view.Execution.Status == "completed" || statuses["compose-greeting"] == orgsdk.NodeStatusCompleted {
				t.Fatalf("Hello demo completed before compose-greeting running was observable: %#v", view.SemanticProjection.Nodes)
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for compose-greeting running: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func assertComposeGreetingStillRunning(t *testing.T, ctx context.Context, control *service.ControlPlane, auth service.AuthenticatedContext, id string) {
	t.Helper()
	view, err := control.GetInvocation(ctx, auth, id)
	if err != nil || view.SemanticProjection == nil {
		t.Fatalf("read running Hello projection: view=%#v err=%v", view, err)
	}
	statuses := projectionStatuses(view.SemanticProjection.Nodes)
	if statuses["compose-greeting"] != orgsdk.NodeStatusRunning || view.Execution.Status == "completed" {
		t.Fatalf("compose-greeting did not remain observable as running: execution=%s nodes=%#v", view.Execution.Status, view.SemanticProjection.Nodes)
	}
}

func projectionStatuses(nodes []orgsdk.NodeProjection) map[string]orgsdk.NodeStatus {
	statuses := make(map[string]orgsdk.NodeStatus, len(nodes))
	for _, node := range nodes {
		statuses[node.TemplateID] = node.Status
	}
	return statuses
}

func assertExecutionVersion(t *testing.T, view service.InvocationView, wantVersion, wantName string) {
	t.Helper()
	if view.Invocation.SelectedVersion != wantVersion {
		t.Fatalf("selected version = %q, want %q", view.Invocation.SelectedVersion, wantVersion)
	}
	if view.WorkerVersion.Version != wantVersion || view.WorkerVersion.Description == "" {
		t.Fatalf("Run did not expose selected WorkerVersion description: %#v", view.WorkerVersion)
	}
	var result struct {
		Message        string `json:"message"`
		WorkerVersion  string `json:"workerVersion"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if err := json.Unmarshal([]byte(view.Execution.Result), &result); err != nil {
		t.Fatalf("decode Workflow result %q: %v", view.Execution.Result, err)
	}
	if result.WorkerVersion != wantVersion {
		t.Fatalf("executed Worker version = %q, want %q", result.WorkerVersion, wantVersion)
	}
	if result.Message != "Hello, "+wantName+"!" {
		t.Fatalf("greeting = %q, want name %q", result.Message, wantName)
	}
	if len(result.IdempotencyKey) != 64 {
		t.Fatalf("idempotency key = %q", result.IdempotencyKey)
	}
}

func requireEnvironment(t *testing.T, ctx context.Context, root string) {
	t.Helper()
	for _, command := range []string{"docker", "kind", "kubectl", "make"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Fatalf("E2E prerequisite %s: %v", command, err)
		}
	}
	runChecked(t, ctx, "docker", "info")
	clusters := runChecked(t, ctx, "kind", "get", "clusters")
	if !containsLine(clusters, "org") {
		t.Fatal("E2E prerequisite: kind cluster org is missing")
	}
	contexts := runChecked(t, ctx, "kubectl", "config", "get-contexts", "-o", "name")
	if !containsLine(contexts, "kind-org") {
		t.Fatal("E2E prerequisite: Kubernetes context kind-org is missing")
	}
	connection, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", "127.0.0.1:7233")
	if err != nil {
		t.Fatalf("E2E prerequisite Temporal 127.0.0.1:7233: %v", err)
	}
	connection.Close()
}

func runChecked(t *testing.T, ctx context.Context, name string, args ...string) string {
	t.Helper()
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("E2E prerequisite %s %s: %v: %s", name, strings.Join(args, " "), err, output)
	}
	return string(output)
}
func containsLine(value, line string) bool {
	for _, candidate := range strings.Split(value, "\n") {
		if strings.TrimSpace(candidate) == line {
			return true
		}
	}
	return false
}
func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve E2E source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
func (r *acceptanceRun) command(name string, args ...string) {
	r.t.Helper()
	output, err := exec.CommandContext(r.ctx, name, args...).CombinedOutput()
	if err != nil {
		r.t.Fatalf("%s %s: %v: %s", name, strings.Join(args, " "), err, output)
	}
}
