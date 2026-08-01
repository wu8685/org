package orgsdk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkerConfigRequiresPlatformRoutingAndManifestIdentity(t *testing.T) {
	valid := WorkerConfig{
		TemporalAddress: "127.0.0.1:7233", TemporalNamespace: "default",
		TaskQueue: "org-tenant-worker", DeploymentName: "org-tenant-worker", BuildID: "v1",
		ManifestDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	valid.TaskQueue = ""
	if err := valid.Validate(); err == nil || !strings.Contains(err.Error(), "Task Queue") {
		t.Fatalf("missing routing error = %v", err)
	}
}

func TestHTTPBootstrapRegistrarSendsWorkloadEvidenceWithoutTargetIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer opaque" || request.Header.Get("X-Org-Workload-Token") != "bound-sa-token" || request.Header.Get("X-Org-Pod-UID") != "pod-uid" {
			t.Errorf("bootstrap headers = %#v", request.Header)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"state":"accepted","receiptId":"reg-1"}`))
	}))
	defer server.Close()
	result, err := (HTTPBootstrapRegistrar{}).Register(context.Background(), BootstrapConfig{Endpoint: server.URL, Credential: "opaque", WorkloadToken: "bound-sa-token", PodUID: "pod-uid"}, BootstrapRegistrationRequest{ManifestDigest: "sha256:" + strings.Repeat("a", 64), Contract: json.RawMessage(`{}`), BuildID: "v1"})
	if err != nil || result.State != BootstrapAccepted {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestHostedWorkerConstructsContractAndRequiresAcceptedRegistrationBeforeRuntime(t *testing.T) {
	definition := Definition{
		Name: "hello", Bounds: RuntimeBounds{MaxInstancesPerFanOut: 2, MaxRuntimeNodes: 3, MaxProjectionBytes: 4096},
		Templates: []NodeTemplate{{ID: "prepare", Label: "Prepare", Type: NodeTypeActivity, Activity: &ActivityPolicy{SideEffect: SideEffectNone, Retry: RetryPolicy{MaximumAttempts: 1, StartToCloseTimeout: time.Second}}}},
	}
	workflowDefinition, err := NewWorkflowDefinition("HelloWorkflow", "v1", definition, func(_ *WorkflowContext, input string) (string, error) { return input, nil })
	if err != nil {
		t.Fatal(err)
	}
	registrar := &recordingBootstrapRegistrar{result: BootstrapRegistrationResult{State: BootstrapAccepted, ReceiptID: "reg-1"}}
	err = RunHostedWorker(context.Background(), HostedWorkerConfig{
		Worker:    WorkerConfig{TemporalAddress: "127.0.0.1:1", TemporalNamespace: "default", TaskQueue: "org-acme-hello", DeploymentName: "org-acme-hello", BuildID: "v1"},
		Bootstrap: BootstrapConfig{Credential: "secret", Registrar: registrar},
	}, workflowDefinition)
	if err == nil {
		t.Fatal("expected Temporal dial failure after accepted registration")
	}
	if registrar.calls != 1 || registrar.request.BuildID != "v1" || registrar.request.ManifestDigest == "" || !json.Valid(registrar.request.Contract) {
		t.Fatalf("registration request = %#v calls=%d", registrar.request, registrar.calls)
	}
	if strings.Contains(string(registrar.request.Contract), "tenantId") || strings.Contains(string(registrar.request.Contract), "workerName") {
		t.Fatalf("SDK attempted to choose bootstrap target: %s", registrar.request.Contract)
	}
}

func TestHostedWorkerStopsOnRejectedRegistration(t *testing.T) {
	definition := Definition{Name: "hello", Bounds: RuntimeBounds{MaxInstancesPerFanOut: 1, MaxRuntimeNodes: 1, MaxProjectionBytes: 4096}, Templates: []NodeTemplate{{ID: "done", Label: "Done", Type: NodeTypeSemantic}}}
	workflowDefinition, err := NewWorkflowDefinition("HelloWorkflow", "v1", definition, func(_ *WorkflowContext, input string) (string, error) { return input, nil })
	if err != nil {
		t.Fatal(err)
	}
	registrar := &recordingBootstrapRegistrar{result: BootstrapRegistrationResult{State: BootstrapRejected, Reason: "contract-invalid"}}
	err = RunHostedWorker(context.Background(), HostedWorkerConfig{
		Worker:    WorkerConfig{TemporalAddress: "127.0.0.1:7233", TemporalNamespace: "default", TaskQueue: "org-acme-hello", DeploymentName: "org-acme-hello", BuildID: "v1"},
		Bootstrap: BootstrapConfig{Credential: "secret", Registrar: registrar},
	}, workflowDefinition)
	if !errors.Is(err, ErrBootstrapRegistrationRejected) {
		t.Fatalf("rejection error = %v", err)
	}
}

type recordingBootstrapRegistrar struct {
	calls   int
	request BootstrapRegistrationRequest
	result  BootstrapRegistrationResult
}

func (r *recordingBootstrapRegistrar) Register(_ context.Context, _ BootstrapConfig, request BootstrapRegistrationRequest) (BootstrapRegistrationResult, error) {
	r.calls++
	r.request = request
	return r.result, nil
}

func TestSDKRegistrationsHideRawTemporalRegistration(t *testing.T) {
	policy := ActivityPolicy{SideEffect: SideEffectNone, Retry: RetryPolicy{MaximumAttempts: 1, StartToCloseTimeout: time.Second}}
	activity := NewActivity("prepare", policy, func(_ ActivityContext, input string) (string, error) { return input, nil })
	contract := Definition{
		Name: "hello", Bounds: RuntimeBounds{MaxInstancesPerFanOut: 2, MaxRuntimeNodes: 3, MaxProjectionBytes: 4096},
		Templates: []NodeTemplate{{ID: "prepare", Label: "Prepare", Type: NodeTypeActivity, Activity: &policy}},
	}
	workflowDefinition, err := NewWorkflowDefinition("HelloWorkflow", "v1", contract, func(ctx *WorkflowContext, input string) (string, error) {
		_, output, err := ExecuteActivity(ctx, activity, "singleton", NodeRef{}, nil, input, "")
		return output, err
	})
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordingRegistrationSink{}
	if err := registerAll(sink, []Registration{activity, workflowDefinition}); err != nil {
		t.Fatal(err)
	}
	if len(sink.activities) != 1 || sink.activities[0] != "prepare" || len(sink.workflows) != 1 || sink.workflows[0] != "HelloWorkflow" {
		t.Fatalf("registrations activities=%v workflows=%v", sink.activities, sink.workflows)
	}
}

type recordingRegistrationSink struct {
	activities []string
	workflows  []string
}

func (r *recordingRegistrationSink) registerActivity(name string, _ any) error {
	r.activities = append(r.activities, name)
	return nil
}

func (r *recordingRegistrationSink) registerWorkflow(name string, _ any) error {
	r.workflows = append(r.workflows, name)
	return nil
}
