package console

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/internal/service"
)

const maxJSONBodyBytes = 1 << 20

type Identity struct {
	Auth              service.AuthenticatedContext
	TenantDisplayName string
	CSRFToken         string
}

type Authenticator interface {
	Authenticate(*http.Request) (Identity, error)
}

// StaticAuthenticator is intended for the loopback-bound local development
// server. Tenant identity comes exclusively from server configuration.
type StaticAuthenticator struct {
	Identity Identity
}

func (a StaticAuthenticator) Authenticate(*http.Request) (Identity, error) {
	if a.Identity.Auth.PrincipalID == "" || a.Identity.Auth.TenantID == "" || a.Identity.Auth.TenantSlug == "" || a.Identity.Auth.AuthenticationMethod == "" {
		return Identity{}, service.ErrUnauthenticated
	}
	return a.Identity, nil
}

type ControlPlane interface {
	CreateWorker(context.Context, service.AuthenticatedContext, service.CreateWorkerRequest) (domain.Worker, error)
	ListWorkers(context.Context, service.AuthenticatedContext) ([]domain.Worker, error)
	GetWorker(context.Context, service.AuthenticatedContext, string) (service.WorkerView, error)
	ListWorkerVersions(context.Context, service.AuthenticatedContext, string) ([]domain.WorkerVersion, error)
	GetWorkerVersion(context.Context, service.AuthenticatedContext, string, string) (domain.WorkerVersion, error)
	ListWorkflowCatalog(context.Context, service.AuthenticatedContext) ([]service.WorkflowCatalogItem, error)
	ListInvocations(context.Context, service.AuthenticatedContext, service.InvocationFilter) ([]domain.Invocation, error)
	GetInvocation(context.Context, service.AuthenticatedContext, string) (service.InvocationView, error)
	ListRunActions(context.Context, service.AuthenticatedContext, string) ([]domain.ActionOperation, error)
	GetOverview(context.Context, service.AuthenticatedContext) (service.Overview, error)
	PublishVersion(context.Context, service.AuthenticatedContext, domain.WorkerVersionRequest) (domain.WorkerVersion, error)
	UpdateWorkerVersionDescription(context.Context, service.AuthenticatedContext, string, string, int64, string) (domain.WorkerVersion, error)
	Start(context.Context, service.AuthenticatedContext, service.StartRequest) (domain.Invocation, error)
	Cancel(context.Context, service.AuthenticatedContext, string) error
	Act(context.Context, service.AuthenticatedContext, service.RunActionRequest) (domain.ActionOperation, error)
	ReconcileRunActions(context.Context, service.AuthenticatedContext, string) (int, error)
}

type Config struct {
	Authenticator  Authenticator
	ControlPlane   ControlPlane
	PublishTimeout time.Duration
}

type server struct {
	authenticator  Authenticator
	controlPlane   ControlPlane
	publishTimeout time.Duration
	operationMu    sync.RWMutex
	operations     map[string]publishOperation
}

func New(config Config) http.Handler {
	timeout := config.PublishTimeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	return &server{authenticator: config.Authenticator, controlPlane: config.ControlPlane, publishTimeout: timeout, operations: map[string]publishOperation{}}
}

func (s *server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	requestID := newRequestID()
	response.Header().Set("X-Request-ID", requestID)
	if s.authenticator == nil || s.controlPlane == nil {
		writeError(response, requestID, errors.New("console is not configured"))
		return
	}
	identity, err := s.authenticator.Authenticate(request)
	if err != nil {
		writeError(response, requestID, err)
		return
	}
	identity.Auth.RequestID = requestID
	if serveAsset(response, request) {
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodOptions {
		if identity.CSRFToken == "" || request.Header.Get("X-CSRF-Token") != identity.CSRFToken {
			writeAPIError(response, http.StatusForbidden, "permission_denied", "CSRF token is missing or invalid", requestID)
			return
		}
	}
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/session":
		s.session(response, requestID, identity)
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/overview":
		s.overview(response, request, requestID, identity)
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/workers":
		s.listWorkers(response, request, requestID, identity)
	case request.Method == http.MethodPost && request.URL.Path == "/api/v1/workers":
		s.createWorker(response, request, requestID, identity)
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/workflows":
		s.listWorkflows(response, request, requestID, identity)
	case request.Method == http.MethodGet && request.URL.Path == "/api/v1/runs":
		s.listRuns(response, request, requestID, identity)
	case request.Method == http.MethodPost && runStartPath(request.URL.Path):
		workerName, workflow := runStartPathParts(request.URL.Path)
		s.startRun(response, request, requestID, identity, workerName, workflow)
	case request.Method == http.MethodPost && runCancelPath(request.URL.Path) != "":
		s.cancelRun(response, request, requestID, identity, runCancelPath(request.URL.Path))
	case request.Method == http.MethodPost && runActionPath(request.URL.Path):
		runID, nodeID, action := runActionPathParts(request.URL.Path)
		s.actOnRun(response, request, requestID, identity, runID, nodeID, action)
	case request.Method == http.MethodGet && actionOperationPath(request.URL.Path):
		runID, operationID := actionOperationPathParts(request.URL.Path)
		s.getActionOperation(response, request, requestID, identity, runID, operationID)
	case request.Method == http.MethodGet && operationPath(request.URL.Path) != "":
		s.getPublishOperation(response, requestID, identity, operationPath(request.URL.Path))
	case request.Method == http.MethodPost && versionCollectionPath(request.URL.Path) != "":
		s.publishVersion(response, request, requestID, identity, versionCollectionPath(request.URL.Path))
	case request.Method == http.MethodPatch && descriptionPath(request.URL.Path):
		workerName, version := descriptionPathParts(request.URL.Path)
		s.updateDescription(response, request, requestID, identity, workerName, version)
	case request.Method == http.MethodGet && workerPath(request.URL.Path, "") != "":
		s.workerResource(response, request, requestID, identity)
	case request.Method == http.MethodGet && runPath(request.URL.Path) != "":
		s.runDetail(response, request, requestID, identity, runPath(request.URL.Path))
	case request.Method == http.MethodGet:
		if page, ok := consoleRoute(request.URL.Path); ok {
			s.serveConsole(response, requestID, identity, page)
			return
		}
		writeAPIError(response, http.StatusNotFound, "not_found", "Resource not found", requestID)
	default:
		writeAPIError(response, http.StatusNotFound, "not_found", "Resource not found", requestID)
	}
}

type publishOperation struct {
	ID            string
	TenantID      string
	State         string
	WorkerVersion *domain.WorkerVersion
	ErrorCode     string
	ErrorMessage  string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type publishVersionInput struct {
	Version       string                  `json:"version"`
	Description   string                  `json:"description"`
	Image         string                  `json:"image"`
	VersionConfig json.RawMessage         `json:"versionConfig,omitempty"`
	Runtime       publishRuntime          `json:"runtime"`
	Source        domain.SourceProvenance `json:"source"`
}

type publishRuntime struct {
	CPU         string                `json:"cpu"`
	Memory      string                `json:"memory"`
	Environment []domain.EnvReference `json:"environment,omitempty"`
}

func (s *server) publishVersion(response http.ResponseWriter, request *http.Request, requestID string, identity Identity, workerName string) {
	var input publishVersionInput
	if err := decodeJSON(response, request, &input); err != nil {
		writeAPIError(response, http.StatusBadRequest, "validation_failed", err.Error(), requestID)
		return
	}
	command := domain.WorkerVersionRequest{
		WorkerName: workerName, Version: input.Version, Description: input.Description, Image: input.Image,
		VersionConfig: input.VersionConfig,
		Runtime:       domain.RuntimeSpec{CPU: input.Runtime.CPU, Memory: input.Runtime.Memory, Environment: input.Runtime.Environment}, Source: input.Source,
	}
	now := time.Now().UTC()
	operation := publishOperation{ID: "pub-" + strings.TrimPrefix(newRequestID(), "req-"), TenantID: identity.Auth.TenantID, State: "running", CreatedAt: now, UpdatedAt: now}
	s.operationMu.Lock()
	s.operations[operation.ID] = operation
	s.operationMu.Unlock()
	auth := identity.Auth
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), s.publishTimeout)
		defer cancel()
		version, err := s.controlPlane.PublishVersion(ctx, auth, command)
		s.operationMu.Lock()
		defer s.operationMu.Unlock()
		current := s.operations[operation.ID]
		current.UpdatedAt = time.Now().UTC()
		if err != nil {
			_, current.ErrorCode, current.ErrorMessage = httpError(err)
			current.State = "failed"
		} else {
			current.State, current.WorkerVersion = "succeeded", &version
		}
		s.operations[operation.ID] = current
	}()
	location := "/api/v1/operations/" + operation.ID
	response.Header().Set("Location", location)
	writeJSON(response, http.StatusAccepted, map[string]any{"requestId": requestID, "operation": operationResponse(operation, location)})
}

func (s *server) getPublishOperation(response http.ResponseWriter, requestID string, identity Identity, operationID string) {
	s.operationMu.RLock()
	operation, ok := s.operations[operationID]
	s.operationMu.RUnlock()
	if !ok || operation.TenantID != identity.Auth.TenantID {
		writeAPIError(response, http.StatusNotFound, "not_found", "Resource not found", requestID)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"requestId": requestID, "operation": operationResponse(operation, "/api/v1/operations/"+operation.ID)})
}

func operationResponse(operation publishOperation, location string) map[string]any {
	response := map[string]any{
		"id": operation.ID, "state": operation.State, "statusUrl": location,
		"createdAt": operation.CreatedAt, "updatedAt": operation.UpdatedAt,
	}
	if operation.WorkerVersion != nil {
		response["workerVersion"] = versionDetail(*operation.WorkerVersion)
	}
	if operation.ErrorCode != "" {
		response["error"] = map[string]string{"code": operation.ErrorCode, "message": operation.ErrorMessage}
	}
	return response
}

func (s *server) updateDescription(response http.ResponseWriter, request *http.Request, requestID string, identity Identity, workerName, version string) {
	ifMatch := request.Header.Get("If-Match")
	if ifMatch == "" {
		writeAPIError(response, http.StatusPreconditionRequired, "precondition_required", "If-Match is required", requestID)
		return
	}
	expectedRevision, ok := parseVersionETag(ifMatch, version)
	if !ok {
		writeAPIError(response, http.StatusPreconditionFailed, "precondition_failed", "If-Match does not identify this WorkerVersion revision", requestID)
		return
	}
	var input struct {
		Description string `json:"description"`
	}
	if err := decodeJSON(response, request, &input); err != nil {
		writeAPIError(response, http.StatusBadRequest, "validation_failed", err.Error(), requestID)
		return
	}
	updated, err := s.controlPlane.UpdateWorkerVersionDescription(request.Context(), identity.Auth, workerName, version, expectedRevision, input.Description)
	if err != nil {
		if errors.Is(err, service.ErrConflict) {
			writeAPIError(response, http.StatusPreconditionFailed, "precondition_failed", "WorkerVersion revision changed", requestID)
			return
		}
		writeError(response, requestID, err)
		return
	}
	response.Header().Set("ETag", versionETag(updated))
	writeJSON(response, http.StatusOK, map[string]any{"requestId": requestID, "workerVersion": versionDetail(updated)})
}

func (s *server) startRun(response http.ResponseWriter, request *http.Request, requestID string, identity Identity, workerName, workflow string) {
	var input struct {
		WorkerVersion string          `json:"workerVersion,omitempty"`
		Input         json.RawMessage `json:"input"`
	}
	if err := decodeJSON(response, request, &input); err != nil {
		writeAPIError(response, http.StatusBadRequest, "validation_failed", err.Error(), requestID)
		return
	}
	invocation, err := s.controlPlane.Start(request.Context(), identity.Auth, service.StartRequest{
		WorkerName: workerName, Workflow: workflow, WorkerVersion: input.WorkerVersion,
		IdempotencyKey: strings.TrimSpace(request.Header.Get("Idempotency-Key")), Input: input.Input,
	})
	if err != nil {
		writeError(response, requestID, err)
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"requestId": requestID, "run": publicInvocation(invocation)})
}

func (s *server) cancelRun(response http.ResponseWriter, request *http.Request, requestID string, identity Identity, runID string) {
	if err := s.controlPlane.Cancel(request.Context(), identity.Auth, runID); err != nil {
		writeError(response, requestID, err)
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]any{
		"requestId": requestID,
		"cancel":    map[string]any{"runId": runID, "state": "requested", "externalEffects": "may-persist"},
	})
}

func (s *server) actOnRun(response http.ResponseWriter, request *http.Request, requestID string, identity Identity, runID, nodeID, action string) {
	operationID := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if operationID == "" {
		writeAPIError(response, http.StatusBadRequest, "validation_failed", "Idempotency-Key is required", requestID)
		return
	}
	ifMatch := request.Header.Get("If-Match")
	if ifMatch == "" {
		writeAPIError(response, http.StatusPreconditionRequired, "precondition_required", "If-Match is required", requestID)
		return
	}
	expectedRevision, ok := parseProjectionETag(ifMatch)
	if !ok {
		writeAPIError(response, http.StatusPreconditionFailed, "projection_conflict", "If-Match is not a projection revision", requestID)
		return
	}
	view, err := s.controlPlane.GetInvocation(request.Context(), identity.Auth, runID)
	if err != nil {
		writeError(response, requestID, err)
		return
	}
	retryOfExisting := false
	if operations, listErr := s.controlPlane.ListRunActions(request.Context(), identity.Auth, runID); listErr == nil {
		for _, operation := range operations {
			if operation.OperationID == operationID && operation.RuntimeNodeID == nodeID && operation.Action == action {
				retryOfExisting = true
				break
			}
		}
	}
	if !retryOfExisting && (view.SemanticProjection == nil || view.SemanticProjection.Revision != expectedRevision) {
		writeAPIError(response, http.StatusPreconditionFailed, "projection_conflict", "Run state changed; refresh before acting", requestID)
		return
	}
	var input struct {
		Input json.RawMessage `json:"input"`
	}
	if err := decodeJSON(response, request, &input); err != nil {
		writeAPIError(response, http.StatusBadRequest, "validation_failed", err.Error(), requestID)
		return
	}
	operation, err := s.controlPlane.Act(request.Context(), identity.Auth, service.RunActionRequest{
		RunID: runID, RuntimeNodeID: nodeID, Action: action, OperationID: operationID, Input: input.Input,
	})
	if err != nil && operation.State != domain.ActionDeliveryUnknown {
		writeError(response, requestID, err)
		return
	}
	writeJSON(response, http.StatusAccepted, map[string]any{"requestId": requestID, "operation": actionOperationResponse(operation)})
}

func (s *server) getActionOperation(response http.ResponseWriter, request *http.Request, requestID string, identity Identity, runID, operationID string) {
	_, _ = s.controlPlane.ReconcileRunActions(request.Context(), identity.Auth, runID)
	operations, err := s.controlPlane.ListRunActions(request.Context(), identity.Auth, runID)
	if err != nil {
		writeError(response, requestID, err)
		return
	}
	for _, operation := range operations {
		if operation.OperationID == operationID {
			writeJSON(response, http.StatusOK, map[string]any{"requestId": requestID, "operation": actionOperationResponse(operation)})
			return
		}
	}
	writeAPIError(response, http.StatusNotFound, "not_found", "Resource not found", requestID)
}

func actionOperationResponse(operation domain.ActionOperation) map[string]any {
	return map[string]any{
		"id": operation.ID, "operationId": operation.OperationID, "state": operation.State,
		"runId": operation.RunID, "runtimeNodeId": operation.RuntimeNodeID, "action": operation.Action,
		"retrySafe": true, "statusUrl": "/api/v1/runs/" + operation.RunID + "/actions/" + operation.OperationID,
		"createdAt": operation.CreatedAt, "updatedAt": operation.UpdatedAt,
	}
}

func (s *server) listWorkers(response http.ResponseWriter, request *http.Request, requestID string, identity Identity) {
	workers, err := s.controlPlane.ListWorkers(request.Context(), identity.Auth)
	if err != nil {
		writeError(response, requestID, err)
		return
	}
	items := make([]map[string]any, 0, len(workers))
	for _, worker := range workers {
		items = append(items, publicWorker(worker))
	}
	writeJSON(response, http.StatusOK, map[string]any{"requestId": requestID, "items": items})
}

func (s *server) overview(response http.ResponseWriter, request *http.Request, requestID string, identity Identity) {
	overview, err := s.controlPlane.GetOverview(request.Context(), identity.Auth)
	if err != nil {
		writeError(response, requestID, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"requestId": requestID, "overview": overview})
}

func (s *server) workerResource(response http.ResponseWriter, request *http.Request, requestID string, identity Identity) {
	parts := pathParts(request.URL.Path)
	workerName := parts[3]
	switch len(parts) {
	case 4:
		view, err := s.controlPlane.GetWorker(request.Context(), identity.Auth, workerName)
		if err != nil {
			writeError(response, requestID, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"requestId": requestID, "worker": publicWorker(view.Worker), "versions": versionSummaries(view.Versions)})
	case 5:
		if parts[4] != "versions" {
			writeAPIError(response, http.StatusNotFound, "not_found", "Resource not found", requestID)
			return
		}
		versions, err := s.controlPlane.ListWorkerVersions(request.Context(), identity.Auth, workerName)
		if err != nil {
			writeError(response, requestID, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"requestId": requestID, "items": versionSummaries(versions)})
	case 6:
		if parts[4] != "versions" {
			writeAPIError(response, http.StatusNotFound, "not_found", "Resource not found", requestID)
			return
		}
		version, err := s.controlPlane.GetWorkerVersion(request.Context(), identity.Auth, workerName, parts[5])
		if err != nil {
			writeError(response, requestID, err)
			return
		}
		etag := versionETag(version)
		if conditionalNotModified(response, request, etag) {
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"requestId": requestID, "workerVersion": versionDetail(version)})
	default:
		writeAPIError(response, http.StatusNotFound, "not_found", "Resource not found", requestID)
	}
}

func (s *server) listWorkflows(response http.ResponseWriter, request *http.Request, requestID string, identity Identity) {
	items, err := s.controlPlane.ListWorkflowCatalog(request.Context(), identity.Auth)
	if err != nil {
		writeError(response, requestID, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"requestId": requestID, "items": items})
}

func (s *server) listRuns(response http.ResponseWriter, request *http.Request, requestID string, identity Identity) {
	query := request.URL.Query()
	items, err := s.controlPlane.ListInvocations(request.Context(), identity.Auth, service.InvocationFilter{
		WorkerName: query.Get("workerName"), Workflow: query.Get("workflow"), WorkerVersion: query.Get("workerVersion"),
	})
	if err != nil {
		writeError(response, requestID, err)
		return
	}
	statusFilter := query.Get("status")
	publicItems := make([]map[string]any, 0, len(items))
	for _, invocation := range items {
		item := publicInvocation(invocation)
		view, viewErr := s.controlPlane.GetInvocation(request.Context(), identity.Auth, invocation.ID)
		executionStatus, projectionStatus := "unavailable", "unavailable"
		if viewErr == nil {
			executionStatus = view.Execution.Status
			if view.SemanticProjection != nil {
				projectionStatus = view.SemanticProjection.Status
			}
		}
		if statusFilter != "" && statusFilter != executionStatus && statusFilter != projectionStatus {
			continue
		}
		item["executionStatus"], item["projectionStatus"] = executionStatus, projectionStatus
		publicItems = append(publicItems, item)
	}
	writeJSON(response, http.StatusOK, map[string]any{"requestId": requestID, "items": publicItems})
}

func (s *server) runDetail(response http.ResponseWriter, request *http.Request, requestID string, identity Identity, runID string) {
	view, err := s.controlPlane.GetInvocation(request.Context(), identity.Auth, runID)
	if err != nil {
		writeError(response, requestID, err)
		return
	}
	actions, err := s.controlPlane.ListRunActions(request.Context(), identity.Auth, runID)
	if err != nil {
		writeError(response, requestID, err)
		return
	}
	revision := uint64(0)
	if view.SemanticProjection != nil {
		revision = view.SemanticProjection.Revision
	}
	etag := `"projection-r` + uintString(revision) + `"`
	if conditionalNotModified(response, request, etag) {
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"requestId": requestID,
		"run": map[string]any{
			"id": view.Invocation.ID, "workerName": view.Invocation.WorkerName, "workflow": view.Invocation.Workflow,
			"selectedVersion": view.Invocation.SelectedVersion, "input": view.Invocation.Input, "actor": view.Invocation.Actor, "createdAt": view.Invocation.CreatedAt,
		},
		"workerVersion": map[string]any{"version": view.WorkerVersion.Version, "description": view.WorkerVersion.Description, "revision": view.WorkerVersion.Revision},
		"execution":     view.Execution, "semanticProjection": view.SemanticProjection,
		"actionOperations": actionSummaries(actions), "temporalDiagnosticsUrl": view.TemporalDiagnosticsURL,
	})
}

func pathParts(path string) []string {
	return strings.Split(strings.Trim(path, "/"), "/")
}

func operationPath(path string) string {
	parts := pathParts(path)
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "operations" && parts[3] != "" {
		return parts[3]
	}
	return ""
}

func versionCollectionPath(path string) string {
	parts := pathParts(path)
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "workers" && parts[3] != "" && parts[4] == "versions" {
		return parts[3]
	}
	return ""
}

func descriptionPath(path string) bool {
	workerName, version := descriptionPathParts(path)
	return workerName != "" && version != ""
}

func descriptionPathParts(path string) (string, string) {
	parts := pathParts(path)
	if len(parts) == 7 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "workers" && parts[3] != "" && parts[4] == "versions" && parts[5] != "" && parts[6] == "description" {
		return parts[3], parts[5]
	}
	return "", ""
}

func runStartPath(path string) bool {
	workerName, workflow := runStartPathParts(path)
	return workerName != "" && workflow != ""
}

func runStartPathParts(path string) (string, string) {
	parts := pathParts(path)
	if len(parts) == 7 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "workers" && parts[3] != "" && parts[4] == "workflows" && parts[5] != "" && parts[6] == "runs" {
		return parts[3], parts[5]
	}
	return "", ""
}

func runCancelPath(path string) string {
	parts := pathParts(path)
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "runs" && parts[3] != "" && parts[4] == "cancel" {
		return parts[3]
	}
	return ""
}

func runActionPath(path string) bool {
	runID, nodeID, action := runActionPathParts(path)
	return runID != "" && nodeID != "" && action != ""
}

func runActionPathParts(path string) (string, string, string) {
	parts := pathParts(path)
	if len(parts) == 8 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "runs" && parts[3] != "" && parts[4] == "nodes" && parts[5] != "" && parts[6] == "actions" && parts[7] != "" {
		return parts[3], parts[5], parts[7]
	}
	return "", "", ""
}

func actionOperationPath(path string) bool {
	runID, operationID := actionOperationPathParts(path)
	return runID != "" && operationID != ""
}

func actionOperationPathParts(path string) (string, string) {
	parts := pathParts(path)
	if len(parts) == 6 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "runs" && parts[3] != "" && parts[4] == "actions" && parts[5] != "" {
		return parts[3], parts[5]
	}
	return "", ""
}

func workerPath(path, suffix string) string {
	parts := pathParts(path)
	if len(parts) < 4 || len(parts) > 6 || parts[0] != "api" || parts[1] != "v1" || parts[2] != "workers" || parts[3] == "" {
		return ""
	}
	return parts[3]
}

func runPath(path string) string {
	parts := pathParts(path)
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "v1" && parts[2] == "runs" && parts[3] != "" {
		return parts[3]
	}
	return ""
}

type versionSummaryResponse struct {
	Version     string                     `json:"version"`
	Description string                     `json:"description"`
	Revision    int64                      `json:"revision"`
	State       domain.WorkerVersionState  `json:"state"`
	Health      domain.WorkerVersionHealth `json:"health"`
	Current     bool                       `json:"current"`
	CreatedAt   any                        `json:"createdAt"`
}

func versionSummaries(versions []domain.WorkerVersion) []versionSummaryResponse {
	out := make([]versionSummaryResponse, 0, len(versions))
	for _, version := range versions {
		out = append(out, versionSummaryResponse{
			Version: version.Version, Description: version.Description, Revision: version.Revision,
			State: version.State, Health: version.Health, Current: version.Current, CreatedAt: version.CreatedAt,
		})
	}
	return out
}

func publicInvocation(invocation domain.Invocation) map[string]any {
	return map[string]any{
		"id": invocation.ID, "workerName": invocation.WorkerName, "workflow": invocation.Workflow,
		"selectedVersion": invocation.SelectedVersion, "input": invocation.Input, "actor": invocation.Actor,
		"createdAt": invocation.CreatedAt,
	}
}

func publicWorker(worker domain.Worker) map[string]any {
	return map[string]any{
		"workerName": worker.Name, "currentVersion": worker.CurrentVersion,
		"createdAt": worker.CreatedAt, "updatedAt": worker.UpdatedAt,
	}
}

func versionDetail(version domain.WorkerVersion) map[string]any {
	verificationStatus := "pending"
	probeStatus := "pending"
	if version.State == domain.WorkerVersionReady && version.Health.KubernetesReady && version.Health.WorkerPolling {
		verificationStatus = "verified"
		probeStatus = "verified"
	} else if version.State == domain.WorkerVersionFailed {
		verificationStatus = "mismatch"
	}
	environment := make([]map[string]string, 0, len(version.Runtime.Environment))
	for _, reference := range version.Runtime.Environment {
		environment = append(environment, map[string]string{"name": reference.Name, "secretRef": reference.Secret, "secretKey": reference.SecretKey})
	}
	return map[string]any{
		"id": version.ID, "workerName": version.WorkerName, "version": version.Version, "description": version.Description,
		"revision": version.Revision, "image": version.Image, "source": version.Source, "state": version.State,
		"health": version.Health, "current": version.Current, "actor": version.Actor, "createdAt": version.CreatedAt, "updatedAt": version.UpdatedAt, "failure": version.Failure,
		"versionConfig": version.VersionConfig,
		"runtime":       map[string]any{"cpu": version.Runtime.CPU, "memory": version.Runtime.Memory, "environment": environment},
		"registration":  map[string]any{"status": version.RegistrationStatus, "registeredAt": version.RegisteredAt},
		"probe": map[string]any{"status": probeStatus, "verifiedAt": func() any {
			if probeStatus == "verified" {
				return version.UpdatedAt
			}
			return nil
		}()},
		"contractVerification": map[string]any{
			"status": verificationStatus, "manifestDigest": version.ManifestDigest,
			"sdkModuleVersion": version.Metadata.SDK.ModuleVersion, "runtimeProtocolVersion": version.Metadata.SDK.RuntimeProtocolVersion,
			"workerBuildId": version.Version, "verifiedAt": version.UpdatedAt,
		},
		"contract": version.Metadata,
	}
}

func actionSummaries(actions []domain.ActionOperation) []map[string]any {
	out := make([]map[string]any, 0, len(actions))
	for _, operation := range actions {
		out = append(out, map[string]any{
			"id": operation.ID, "runId": operation.RunID, "runtimeNodeId": operation.RuntimeNodeID,
			"action": operation.Action, "operationId": operation.OperationID, "state": operation.State,
			"createdAt": operation.CreatedAt, "updatedAt": operation.UpdatedAt,
		})
	}
	return out
}

func versionETag(version domain.WorkerVersion) string {
	return `"version-` + version.Version + `-r` + int64String(version.Revision) + `"`
}

func parseVersionETag(etag, version string) (int64, bool) {
	prefix := `"version-` + version + `-r`
	if !strings.HasPrefix(etag, prefix) || !strings.HasSuffix(etag, `"`) {
		return 0, false
	}
	revisionText := strings.TrimSuffix(strings.TrimPrefix(etag, prefix), `"`)
	revision, err := strconv.ParseInt(revisionText, 10, 64)
	return revision, err == nil && revision > 0
}

func parseProjectionETag(etag string) (uint64, bool) {
	const prefix = `"projection-r`
	if !strings.HasPrefix(etag, prefix) || !strings.HasSuffix(etag, `"`) {
		return 0, false
	}
	revisionText := strings.TrimSuffix(strings.TrimPrefix(etag, prefix), `"`)
	revision, err := strconv.ParseUint(revisionText, 10, 64)
	return revision, err == nil
}

func conditionalNotModified(response http.ResponseWriter, request *http.Request, etag string) bool {
	response.Header().Set("ETag", etag)
	if request.Header.Get("If-None-Match") == etag {
		response.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

func int64String(value int64) string {
	return strconv.FormatInt(value, 10)
}

func uintString(value uint64) string {
	return strconv.FormatUint(value, 10)
}

func (s *server) session(response http.ResponseWriter, requestID string, identity Identity) {
	permissions := make([]string, 0, len(identity.Auth.Permissions))
	for permission, allowed := range identity.Auth.Permissions {
		if allowed {
			permissions = append(permissions, permission)
		}
	}
	sort.Strings(permissions)
	writeJSON(response, http.StatusOK, map[string]any{
		"requestId": requestID,
		"session": map[string]any{
			"principalId": identity.Auth.PrincipalID,
			"tenant": map[string]string{
				"id": identity.Auth.TenantID, "slug": identity.Auth.TenantSlug, "displayName": identity.TenantDisplayName,
			},
			"permissions": permissions,
			"csrfToken":   identity.CSRFToken,
		},
	})
}

func (s *server) createWorker(response http.ResponseWriter, request *http.Request, requestID string, identity Identity) {
	var input struct {
		WorkerName string `json:"workerName"`
	}
	if err := decodeJSON(response, request, &input); err != nil {
		writeAPIError(response, http.StatusBadRequest, "validation_failed", err.Error(), requestID)
		return
	}
	worker, err := s.controlPlane.CreateWorker(request.Context(), identity.Auth, service.CreateWorkerRequest{WorkerName: input.WorkerName})
	if err != nil {
		writeError(response, requestID, err)
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{"requestId": requestID, "worker": worker})
}

func decodeJSON(response http.ResponseWriter, request *http.Request, target any) error {
	reader := http.MaxBytesReader(response, request.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("request body must be valid JSON with only declared fields")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func writeError(response http.ResponseWriter, requestID string, err error) {
	status, code, message := httpError(err)
	writeAPIError(response, status, code, message, requestID)
}

func httpError(err error) (int, string, string) {
	switch {
	case errors.Is(err, service.ErrUnauthenticated):
		return http.StatusUnauthorized, "unauthenticated", "Authentication is required"
	case errors.Is(err, service.ErrPermissionDenied):
		return http.StatusForbidden, "permission_denied", "Permission denied"
	case errors.Is(err, service.ErrNotFound):
		return http.StatusNotFound, "not_found", "Resource not found"
	case errors.Is(err, service.ErrConflict):
		return http.StatusConflict, "conflict", "Resource state conflicts with the request"
	case errors.Is(err, service.ErrTenantQuotaExceeded):
		return http.StatusTooManyRequests, "quota_exceeded", "Tenant quota exceeded"
	default:
		return http.StatusBadRequest, "validation_failed", strings.TrimSpace(err.Error())
	}
}

func writeAPIError(response http.ResponseWriter, status int, code, message, requestID string) {
	writeJSON(response, status, map[string]any{"error": map[string]any{"code": code, "message": message, "requestId": requestID}})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func newRequestID() string {
	var material [12]byte
	if _, err := rand.Read(material[:]); err != nil {
		return "req-unavailable"
	}
	return "req-" + hex.EncodeToString(material[:])
}
