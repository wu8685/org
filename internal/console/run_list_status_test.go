package console

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
	"github.com/wu8685/org/sdk/orgsdk"
)

func TestRunListReturnsSafeLatestSemanticStatusForEveryProductState(t *testing.T) {
	base := time.Date(2026, 8, 2, 3, 0, 0, 0, time.UTC)
	runs := []domain.Invocation{
		{ID: "run-running", State: domain.InvocationRunning, UpdatedAt: base},
		{ID: "run-waiting", State: domain.InvocationRunning, UpdatedAt: base.Add(time.Second)},
		{ID: "run-completed", State: domain.InvocationCompleted, UpdatedAt: base.Add(2 * time.Second)},
		{ID: "run-failed", State: domain.InvocationFailed, UpdatedAt: base.Add(3 * time.Second)},
		{ID: "run-cancelled", State: domain.InvocationCanceled, UpdatedAt: base.Add(4 * time.Second)},
	}
	views := map[string]service.InvocationView{
		"run-running":   runListView(runs[0], 11, "running", orgsdk.NodeProjection{Label: "Compose greeting", Status: orgsdk.NodeStatusRunning, StartedAt: base.Add(5 * time.Second)}),
		"run-waiting":   runListView(runs[1], 12, "running", orgsdk.NodeProjection{Label: "Approve <release>", Status: orgsdk.NodeStatusWaitingForUser, ReasonCode: "secret=must-not-leak", StartedAt: base.Add(6 * time.Second)}),
		"run-completed": runListView(runs[2], 13, "completed", orgsdk.NodeProjection{Label: "Finalize", Status: orgsdk.NodeStatusCompleted, CompletedAt: base.Add(7 * time.Second)}),
		"run-failed":    runListView(runs[3], 14, "failed", orgsdk.NodeProjection{Label: "Execute", Status: orgsdk.NodeStatusFailed, ReasonCode: "customer-token", CompletedAt: base.Add(8 * time.Second)}),
		"run-cancelled": runListView(runs[4], 15, "completed", orgsdk.NodeProjection{Label: "Stop", Status: orgsdk.NodeStatusCanceled, ReasonCode: "private cancel detail", CompletedAt: base.Add(9 * time.Second)}),
	}
	backend := &stubControlPlane{runs: runs, invocationViews: views}
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/runs", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if response.Header().Get("ETag") == "" || response.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("headers=%v", response.Header())
	}
	if strings.Contains(response.Body.String(), "must-not-leak") || strings.Contains(response.Body.String(), "customer-token") || strings.Contains(response.Body.String(), "private cancel detail") || strings.Contains(response.Body.String(), "<release>") {
		t.Fatalf("unsafe projection detail leaked: %s", response.Body.String())
	}
	var body struct {
		Items []struct {
			ID                 string `json:"id"`
			SemanticStatus     string `json:"semanticStatus"`
			BlockReason        string `json:"blockReason"`
			CurrentNodeSummary string `json:"currentNodeSummary"`
			ProjectionRevision uint64 `json:"projectionRevision"`
			SemanticUpdatedAt  string `json:"semanticUpdatedAt"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"run-running": "running", "run-waiting": "waiting-for-user", "run-completed": "completed",
		"run-failed": "failed", "run-cancelled": "canceled",
	}
	for _, item := range body.Items {
		if item.SemanticStatus != want[item.ID] || item.ProjectionRevision == 0 || item.SemanticUpdatedAt == "" {
			t.Errorf("item=%#v wantStatus=%q", item, want[item.ID])
		}
		if item.ID == "run-waiting" && (item.BlockReason != "Waiting for an authorized user action" || item.CurrentNodeSummary != "Approve <release>") {
			t.Errorf("waiting item=%#v", item)
		}
	}

	filtered := httptest.NewRecorder()
	handler.ServeHTTP(filtered, httptest.NewRequest(http.MethodGet, "/api/v1/runs?status=waiting-for-user", nil))
	if filtered.Code != http.StatusOK || !strings.Contains(filtered.Body.String(), `"id":"run-waiting"`) || strings.Contains(filtered.Body.String(), `"id":"run-running"`) {
		t.Fatalf("filtered status=%d body=%s", filtered.Code, filtered.Body.String())
	}
}

func TestRunListConditionalETagTracksProjectionAndTenant(t *testing.T) {
	now := time.Date(2026, 8, 2, 4, 0, 0, 0, time.UTC)
	run := domain.Invocation{ID: "run-1", State: domain.InvocationRunning, UpdatedAt: now}
	view := runListView(run, 1, "running", orgsdk.NodeProjection{Label: "Work", Status: orgsdk.NodeStatusRunning, StartedAt: now})
	backend := &stubControlPlane{runs: []domain.Invocation{run}, invocationViews: map[string]service.InvocationView{"run-1": view}}

	first := httptest.NewRecorder()
	New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend}).ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/v1/runs", nil))
	etagA := first.Header().Get("ETag")
	repeatedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/runs", nil)
	repeatedRequest.Header.Set("If-None-Match", etagA)
	repeated := httptest.NewRecorder()
	New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend}).ServeHTTP(repeated, repeatedRequest)
	if repeated.Code != http.StatusNotModified || repeated.Body.Len() != 0 {
		t.Fatalf("repeat status=%d body=%s", repeated.Code, repeated.Body.String())
	}

	backend.invocationViews["run-1"].SemanticProjection.Nodes[0].StartedAt = now.Add(time.Hour)
	timestampDriftRequest := httptest.NewRequest(http.MethodGet, "/api/v1/runs", nil)
	timestampDriftRequest.Header.Set("If-None-Match", etagA)
	timestampDrift := httptest.NewRecorder()
	New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend}).ServeHTTP(timestampDrift, timestampDriftRequest)
	if timestampDrift.Code != http.StatusNotModified {
		t.Fatalf("same-revision projection timestamp drift changed collection status=%d etag=%q body=%s", timestampDrift.Code, timestampDrift.Header().Get("ETag"), timestampDrift.Body.String())
	}

	backend.invocationViews["run-1"].SemanticProjection.Revision = 2
	changedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/runs", nil)
	changedRequest.Header.Set("If-None-Match", etagA)
	changed := httptest.NewRecorder()
	New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend}).ServeHTTP(changed, changedRequest)
	if changed.Code != http.StatusOK || changed.Header().Get("ETag") == etagA {
		t.Fatalf("changed status=%d etag=%q", changed.Code, changed.Header().Get("ETag"))
	}

	identityB := testIdentity()
	identityB.Auth.TenantID, identityB.Auth.TenantSlug, identityB.TenantDisplayName = "tenant-b", "beta", "Beta"
	tenantB := httptest.NewRecorder()
	New(Config{Authenticator: stubAuthenticator{identity: identityB}, ControlPlane: backend}).ServeHTTP(tenantB, httptest.NewRequest(http.MethodGet, "/api/v1/runs", nil))
	if tenantB.Header().Get("ETag") == changed.Header().Get("ETag") {
		t.Fatalf("collection ETag was not Tenant-bound: %q", tenantB.Header().Get("ETag"))
	}
}

func TestRunListReturnsBoundedSafeFailureSummaryAndTracksItInETag(t *testing.T) {
	now := time.Date(2026, 8, 2, 5, 0, 0, 0, time.UTC)
	run := domain.Invocation{ID: "run-failed", State: domain.InvocationFailed, UpdatedAt: now}
	view := runListView(run, 4, "failed", orgsdk.NodeProjection{Label: "Determine <route>", Status: orgsdk.NodeStatusFailed, CompletedAt: now})
	view.Failure = &domain.RunFailure{
		Code: "invalid_route", Message: strings.Repeat("路", 170) + "<script>alert(1)</script>",
		RuntimeNodeID: "node-aaaaaaaaaaaaaaaa", TemplateID: "determine-route", NodeLabel: "Determine <route>", OccurredAt: now,
	}
	backend := &stubControlPlane{runs: []domain.Invocation{run}, invocationViews: map[string]service.InvocationView{run.ID: view}}
	handler := New(Config{Authenticator: stubAuthenticator{identity: testIdentity()}, ControlPlane: backend})

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/api/v1/runs", nil))
	if first.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", first.Code, first.Body.String())
	}
	var body struct {
		Items []struct {
			ErrorSummary *struct {
				Code, Message, NodeLabel string
				OccurredAt               time.Time
			} `json:"errorSummary"`
		} `json:"items"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].ErrorSummary == nil {
		t.Fatalf("missing errorSummary: %s", first.Body.String())
	}
	summary := body.Items[0].ErrorSummary
	if summary.Code != "invalid_route" || summary.NodeLabel != "Determine <route>" || summary.OccurredAt != now {
		t.Fatalf("summary=%#v", summary)
	}
	if len([]rune(summary.Message)) != 160 || strings.Contains(summary.Message, "<script>") {
		t.Fatalf("message must be safely bounded before markup rendering: %q", summary.Message)
	}

	etag := first.Header().Get("ETag")
	changedView := backend.invocationViews[run.ID]
	changedFailure := *changedView.Failure
	changedFailure.Message = "A different safe message"
	changedView.Failure = &changedFailure
	backend.invocationViews[run.ID] = changedView
	request := httptest.NewRequest(http.MethodGet, "/api/v1/runs", nil)
	request.Header.Set("If-None-Match", etag)
	changed := httptest.NewRecorder()
	handler.ServeHTTP(changed, request)
	if changed.Code != http.StatusOK || changed.Header().Get("ETag") == etag {
		t.Fatalf("failure change must invalidate list ETag: status=%d etag=%q body=%s", changed.Code, changed.Header().Get("ETag"), changed.Body.String())
	}
}

func runListView(invocation domain.Invocation, revision uint64, status string, node orgsdk.NodeProjection) service.InvocationView {
	node.RuntimeNodeID = "node-aaaaaaaaaaaaaaaa"
	projection := &orgsdk.Projection{
		Revision: revision, Status: status, Nodes: []orgsdk.NodeProjection{node}, CurrentNodeIDs: []string{node.RuntimeNodeID},
		RecentEvents: []orgsdk.ProjectionEvent{{Timestamp: node.StartedAt}},
	}
	return service.InvocationView{Invocation: invocation, Execution: service.ExecutionState{Status: string(invocation.State)}, SemanticProjection: projection}
}
