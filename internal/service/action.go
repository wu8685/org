package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wu8685/org/internal/domain"
	"github.com/wu8685/org/sdk/orgsdk"
)

type ActionDeliveryState = domain.ActionDeliveryState

const (
	ActionDeliveryReserved  = domain.ActionDeliveryReserved
	ActionDeliveryDelivered = domain.ActionDeliveryDelivered
	ActionDeliveryUnknown   = domain.ActionDeliveryUnknown
	ActionDeliveryAccepted  = domain.ActionDeliveryAccepted
	ActionDeliveryRejected  = domain.ActionDeliveryRejected
)

type RunActionRequest struct {
	RunID         string          `json:"runId"`
	RuntimeNodeID string          `json:"runtimeNodeId"`
	Action        string          `json:"action"`
	OperationID   string          `json:"operationId"`
	Input         json.RawMessage `json:"input"`
}

type GatewayPrincipal struct {
	ID       string `json:"id"`
	TenantID string `json:"tenantId"`
}

type GatewayActionEnvelope struct {
	OperationID string           `json:"operationId"`
	NodeID      string           `json:"nodeId"`
	Action      string           `json:"action"`
	Input       json.RawMessage  `json:"input"`
	Principal   GatewayPrincipal `json:"principal"`
	RequestID   string           `json:"requestId"`
	OccurredAt  time.Time        `json:"occurredAt"`
}

func (c *ControlPlane) Act(ctx context.Context, auth AuthenticatedContext, request RunActionRequest) (result domain.ActionOperation, err error) {
	tenant, err := c.authenticatedTenant(auth)
	if err != nil {
		return domain.ActionOperation{}, err
	}
	references := map[string]string{
		"runId": request.RunID, "runtimeNodeId": request.RuntimeNodeID,
		"action": request.Action, "operationId": request.OperationID,
	}
	if tenant.Status != domain.TenantActive {
		c.auditAllowed(auth, tenant, PermissionRunSignal, "run.action", "invocation", request.RunID, ErrTenantSuspended, references)
		return domain.ActionOperation{}, ErrTenantSuspended
	}
	invocation, ok := c.store.Invocation(tenant.ID, request.RunID)
	if !ok {
		c.auditAllowed(auth, tenant, PermissionRunSignal, "run.action", "invocation", request.RunID, ErrNotFound, references)
		return domain.ActionOperation{}, ErrNotFound
	}
	_, contract, err := c.invocationContract(invocation)
	if err != nil {
		return domain.ActionOperation{}, err
	}
	action, ok := workflowAction(contract, request.Action)
	if !ok {
		return domain.ActionOperation{}, ErrNotFound
	}
	if !auth.Permissions[action.RequiredPermission] {
		c.audit(auth, tenant, action.RequiredPermission, "run.action", "invocation", request.RunID, "denied", "denied", "permission_denied", references)
		return domain.ActionOperation{}, ErrPermissionDenied
	}
	defer func() {
		c.auditAllowed(auth, tenant, action.RequiredPermission, "run.action", "invocation", request.RunID, err, references)
	}()
	if request.OperationID == "" || request.RuntimeNodeID == "" || request.Action == "" {
		return domain.ActionOperation{}, errors.New("run, runtime node, action, and operation ID are required")
	}
	canonicalInput, err := canonicalJSON(request.Input)
	if err != nil {
		return domain.ActionOperation{}, fmt.Errorf("action input schema: %w", err)
	}
	if err := validateJSONSchema(action.InputSchema, canonicalInput); err != nil {
		return domain.ActionOperation{}, fmt.Errorf("action input schema: %w", err)
	}
	sum := sha256.Sum256(canonicalInput)
	payloadDigest := hex.EncodeToString(sum[:])
	references["payloadDigest"] = payloadDigest

	c.mu.Lock()
	defer c.mu.Unlock()
	operation, exists := c.store.ActionOperation(tenant.ID, request.RunID, request.RuntimeNodeID, request.Action, request.OperationID)
	if exists {
		existing := operation
		if existing.PayloadDigest != payloadDigest {
			return domain.ActionOperation{}, ErrConflict
		}
		switch existing.State {
		case domain.ActionDeliveryDelivered, domain.ActionDeliveryAccepted, domain.ActionDeliveryRejected:
			result = existing
			return existing, nil
		}
	}
	projectionJSON, err := c.executor.Query(ctx, invocation, contract.ProjectionQuery, nil)
	if err != nil {
		return domain.ActionOperation{}, err
	}
	var projection orgsdk.Projection
	if err := json.Unmarshal(projectionJSON, &projection); err != nil {
		return domain.ActionOperation{}, fmt.Errorf("invalid Worker projection: %w", err)
	}
	references["projectionRevision"] = fmt.Sprint(projection.Revision)
	if exists {
		if next, found := actionOperationOutcome(projection, operation); found {
			operation.State, operation.UpdatedAt = next, time.Now().UTC()
			if err := c.store.SaveActionOperation(tenant.ID, operation); err != nil {
				return domain.ActionOperation{}, err
			}
			result = operation
			return operation, nil
		}
	}
	if err := validateActionProjection(projection, invocation, request, action); err != nil {
		return domain.ActionOperation{}, err
	}
	now := time.Now().UTC()
	if !exists {
		operation = domain.ActionOperation{
			ID: newID("act"), TenantID: tenant.ID, RunID: request.RunID, RuntimeNodeID: request.RuntimeNodeID,
			Action: request.Action, OperationID: request.OperationID, PayloadDigest: payloadDigest,
			State: domain.ActionDeliveryReserved, PrincipalID: auth.PrincipalID, RequestID: auth.RequestID,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := c.store.SaveActionOperation(tenant.ID, operation); err != nil {
			return domain.ActionOperation{}, err
		}
	}
	envelope := GatewayActionEnvelope{
		OperationID: request.OperationID, NodeID: request.RuntimeNodeID, Action: request.Action,
		Input: canonicalInput, Principal: GatewayPrincipal{ID: operation.PrincipalID, TenantID: tenant.ID},
		RequestID: operation.RequestID, OccurredAt: operation.CreatedAt,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return domain.ActionOperation{}, err
	}
	if signalErr := c.executor.Signal(ctx, invocation, orgsdk.ReservedActionSignal, encoded); signalErr != nil {
		operation.State, operation.UpdatedAt = domain.ActionDeliveryUnknown, time.Now().UTC()
		_ = c.store.SaveActionOperation(tenant.ID, operation)
		result = operation
		return operation, signalErr
	}
	operation.State, operation.UpdatedAt = domain.ActionDeliveryDelivered, time.Now().UTC()
	if err := c.store.SaveActionOperation(tenant.ID, operation); err != nil {
		return domain.ActionOperation{}, err
	}
	result = operation
	return operation, nil
}

func (c *ControlPlane) ReconcileRunActions(ctx context.Context, auth AuthenticatedContext, runID string) (updated int, err error) {
	tenant, err := c.authorize(auth, PermissionRunRead, "run.action.reconcile", "invocation", runID)
	if err != nil {
		return 0, err
	}
	defer func() {
		c.auditAllowed(auth, tenant, PermissionRunRead, "run.action.reconcile", "invocation", runID, err, map[string]string{"updatedOperations": fmt.Sprint(updated)})
	}()
	invocation, ok := c.store.Invocation(tenant.ID, runID)
	if !ok {
		return 0, ErrNotFound
	}
	_, contract, err := c.invocationContract(invocation)
	if err != nil {
		return 0, err
	}
	projectionJSON, err := c.executor.Query(ctx, invocation, contract.ProjectionQuery, nil)
	if err != nil {
		return 0, err
	}
	var projection orgsdk.Projection
	if err := json.Unmarshal(projectionJSON, &projection); err != nil {
		return 0, fmt.Errorf("invalid Worker projection: %w", err)
	}
	if projection.ContractVersion != orgsdk.ContractVersion || projection.WorkflowName != invocation.Workflow || projection.WorkerVersion != invocation.SelectedVersion {
		return 0, errors.New("invalid Worker projection identity")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, outcome := range projection.ActionOutcomes {
		operation, exists := c.store.ActionOperation(tenant.ID, runID, outcome.RuntimeNodeID, outcome.Action, outcome.OperationID)
		if !exists {
			continue
		}
		next := operation.State
		switch outcome.State {
		case orgsdk.ActionOutcomeAccepted, orgsdk.ActionOutcomeDuplicate:
			next = domain.ActionDeliveryAccepted
		case orgsdk.ActionOutcomeRejected, orgsdk.ActionOutcomeExpired:
			next = domain.ActionDeliveryRejected
		default:
			continue
		}
		if operation.State == next {
			continue
		}
		operation.State, operation.UpdatedAt = next, time.Now().UTC()
		if err := c.store.SaveActionOperation(tenant.ID, operation); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

func (c *ControlPlane) authenticatedTenant(auth AuthenticatedContext) (domain.Tenant, error) {
	if auth.PrincipalID == "" || auth.TenantID == "" || auth.AuthenticationMethod == "" {
		return domain.Tenant{}, ErrUnauthenticated
	}
	tenant, ok := c.store.Tenant(auth.TenantID)
	if !ok || tenant.Slug != auth.TenantSlug {
		c.audit(auth, tenant, PermissionRunSignal, "run.action", "invocation", "", "denied", "denied", "permission_denied", nil)
		return domain.Tenant{}, ErrPermissionDenied
	}
	return tenant, nil
}

func workflowAction(contract domain.WorkflowContract, name string) (domain.ActionContract, bool) {
	for _, action := range contract.Actions {
		if action.Name == name {
			return action, true
		}
	}
	return domain.ActionContract{}, false
}

func validateActionProjection(projection orgsdk.Projection, invocation domain.Invocation, request RunActionRequest, action domain.ActionContract) error {
	if projection.ContractVersion != orgsdk.ContractVersion || projection.WorkflowName != invocation.Workflow || projection.WorkerVersion != invocation.SelectedVersion {
		return errors.New("invalid Worker projection identity")
	}
	node := projection.Node(request.RuntimeNodeID)
	if node.RuntimeNodeID == "" || node.TemplateID != action.NodeTemplateID || node.Status != orgsdk.NodeStatusWaitingForUser {
		return ErrConflict
	}
	for _, allowed := range projection.AllowedActions {
		if allowed.RuntimeNodeID == request.RuntimeNodeID && allowed.Name == request.Action {
			return nil
		}
	}
	return ErrConflict
}

func actionOperationOutcome(projection orgsdk.Projection, operation domain.ActionOperation) (domain.ActionDeliveryState, bool) {
	for _, outcome := range projection.ActionOutcomes {
		if outcome.OperationID != operation.OperationID || outcome.RuntimeNodeID != operation.RuntimeNodeID || outcome.Action != operation.Action {
			continue
		}
		switch outcome.State {
		case orgsdk.ActionOutcomeAccepted, orgsdk.ActionOutcomeDuplicate:
			return domain.ActionDeliveryAccepted, true
		case orgsdk.ActionOutcomeRejected, orgsdk.ActionOutcomeExpired:
			return domain.ActionDeliveryRejected, true
		}
	}
	return "", false
}

func canonicalJSON(input json.RawMessage) ([]byte, error) {
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	var value any
	if err := json.Unmarshal(input, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func validateJSONSchema(schema json.RawMessage, input []byte) error {
	if len(schema) == 0 {
		return nil
	}
	var contract map[string]any
	if err := json.Unmarshal(schema, &contract); err != nil {
		return errors.New("invalid declared JSON Schema")
	}
	var value any
	if err := json.Unmarshal(input, &value); err != nil {
		return err
	}
	return validateSchemaValue(contract, value, "$")
}

func validateSchemaValue(schema map[string]any, value any, path string) error {
	kind, _ := schema["type"].(string)
	switch kind {
	case "", "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		required, _ := schema["required"].([]any)
		for _, rawName := range required {
			name, _ := rawName.(string)
			if _, exists := object[name]; !exists {
				return fmt.Errorf("%s.%s is required", path, name)
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		additionalAllowed, hasAdditional := schema["additionalProperties"].(bool)
		for name, child := range object {
			rawChildSchema, declared := properties[name]
			if !declared {
				if hasAdditional && !additionalAllowed {
					return fmt.Errorf("%s.%s is not declared", path, name)
				}
				continue
			}
			childSchema, ok := rawChildSchema.(map[string]any)
			if !ok {
				return errors.New("declared property schema is invalid")
			}
			if err := validateSchemaValue(childSchema, child, path+"."+name); err != nil {
				return err
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s must be a string", path)
		}
		if maximum, ok := schema["maxLength"].(float64); ok && len([]rune(text)) > int(maximum) {
			return fmt.Errorf("%s exceeds maxLength", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", path)
		}
	case "number", "integer":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("%s must be a number", path)
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("%s must be an array", path)
		}
	default:
		return fmt.Errorf("unsupported schema type %q", kind)
	}
	return nil
}

func actionReferenceDigest(value string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(value)))
	return hex.EncodeToString(sum[:])
}
