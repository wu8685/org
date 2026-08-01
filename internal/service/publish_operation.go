package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/wu8685/org/internal/domain"
)

const defaultPublishOperationRetention = 24 * time.Hour

type PublishOperationReservation struct {
	IdempotencyKey string
	PayloadDigest  string
	WorkerName     string
	Version        string
}

func (c *ControlPlane) ReservePublishOperation(_ context.Context, auth AuthenticatedContext, request PublishOperationReservation) (operation domain.PublishOperation, created bool, err error) {
	tenant, err := c.authorize(auth, PermissionWorkerDeploy, "worker.version.publish.reserve", "publishOperation", "")
	if err != nil {
		return domain.PublishOperation{}, false, err
	}
	key := request.IdempotencyKey
	if err := validatePublishIdempotencyKey(key); err != nil {
		c.auditAllowed(auth, tenant, PermissionWorkerDeploy, "worker.version.publish.reserve", "publishOperation", "", err, nil)
		return domain.PublishOperation{}, false, err
	}
	if request.PayloadDigest == "" || request.WorkerName == "" || request.Version == "" {
		err := errors.New("publish payload digest, Worker name, and version are required")
		c.auditAllowed(auth, tenant, PermissionWorkerDeploy, "worker.version.publish.reserve", "publishOperation", "", err, nil)
		return domain.PublishOperation{}, false, err
	}
	now := c.publishNow()
	operation = domain.PublishOperation{
		ID: newID("pub"), TenantID: tenant.ID, PrincipalID: auth.PrincipalID,
		IdempotencyKeyHash: publishIdempotencyKeyHash(key), PayloadDigest: request.PayloadDigest,
		WorkerName: request.WorkerName, Version: request.Version, State: domain.PublishOperationRunning,
		RequestID: auth.RequestID, CreatedAt: now, UpdatedAt: now,
	}
	operation, created, err = c.store.ReservePublishOperation(operation, now)
	references := map[string]string{
		"workerName": request.WorkerName, "version": request.Version, "payloadDigest": request.PayloadDigest,
		"idempotencyKeyHash": operation.IdempotencyKeyHash,
	}
	if operation.ID != "" {
		references["operationId"] = operation.ID
	}
	if err != nil {
		references["idempotencyOutcome"] = "conflict"
	} else if created {
		references["idempotencyOutcome"] = "reserved"
	} else {
		references["idempotencyOutcome"] = "replayed"
	}
	c.auditAllowed(auth, tenant, PermissionWorkerDeploy, "worker.version.publish.reserve", "publishOperation", operation.ID, err, references)
	return operation, created, err
}

func (c *ControlPlane) CompletePublishOperation(_ context.Context, auth AuthenticatedContext, operationID string, version domain.WorkerVersion, errorCode, errorMessage string) (domain.PublishOperation, error) {
	tenant, err := c.authorize(auth, PermissionWorkerDeploy, "worker.version.publish.complete", "publishOperation", operationID)
	if err != nil {
		return domain.PublishOperation{}, err
	}
	operation, ok := c.store.PublishOperation(tenant.ID, operationID)
	if !ok || operation.PrincipalID != auth.PrincipalID {
		return domain.PublishOperation{}, ErrNotFound
	}
	if operation.State != domain.PublishOperationRunning {
		return operation, nil
	}
	operation.UpdatedAt = c.publishNow()
	operation.ExpiresAt = operation.UpdatedAt.Add(c.publishOperationRetention())
	if errorCode != "" {
		operation.State, operation.ErrorCode, operation.ErrorMessage = domain.PublishOperationFailed, errorCode, errorMessage
	} else {
		operation.State, operation.WorkerVersion = domain.PublishOperationSucceeded, &version
	}
	if err := c.store.SavePublishOperation(tenant.ID, operation); err != nil {
		return domain.PublishOperation{}, err
	}
	return operation, nil
}

func (c *ControlPlane) GetPublishOperation(_ context.Context, auth AuthenticatedContext, operationID string) (domain.PublishOperation, error) {
	tenant, err := c.authorize(auth, PermissionWorkerRead, "worker.version.publish.operation.read", "publishOperation", operationID)
	if err != nil {
		return domain.PublishOperation{}, err
	}
	operation, ok := c.store.PublishOperation(tenant.ID, operationID)
	if !ok || publishOperationExpired(operation, c.publishNow()) {
		return domain.PublishOperation{}, ErrNotFound
	}
	return operation, nil
}

func validatePublishIdempotencyKey(key string) error {
	if len(key) == 0 || len(key) > 200 {
		return errors.New("Idempotency-Key must contain 1 to 200 visible ASCII characters")
	}
	for _, character := range []byte(key) {
		if character < 0x21 || character > 0x7e {
			return errors.New("Idempotency-Key must contain 1 to 200 visible ASCII characters")
		}
	}
	return nil
}

func publishIdempotencyKeyHash(key string) string {
	digest := sha256.Sum256([]byte(key))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func (c *ControlPlane) publishNow() time.Time {
	if c.cfg.Now != nil {
		return c.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func (c *ControlPlane) publishOperationRetention() time.Duration {
	if c.cfg.PublishOperationRetention > 0 {
		return c.cfg.PublishOperationRetention
	}
	return defaultPublishOperationRetention
}

func publishOperationExpired(operation domain.PublishOperation, now time.Time) bool {
	return operation.State != domain.PublishOperationRunning && !operation.ExpiresAt.IsZero() && !operation.ExpiresAt.After(now)
}

func samePublishOperationScope(left, right domain.PublishOperation) bool {
	return left.TenantID == right.TenantID && left.PrincipalID == right.PrincipalID && left.IdempotencyKeyHash == right.IdempotencyKeyHash
}

func validatePublishOperation(operation domain.PublishOperation) error {
	if operation.ID == "" || operation.TenantID == "" || operation.PrincipalID == "" || operation.IdempotencyKeyHash == "" || operation.PayloadDigest == "" || operation.WorkerName == "" || operation.Version == "" {
		return errors.New("publish operation identity is required")
	}
	return nil
}

func (s *MemoryStore) ReservePublishOperation(candidate domain.PublishOperation, now time.Time) (domain.PublishOperation, bool, error) {
	if err := validatePublishOperation(candidate); err != nil {
		return domain.PublishOperation{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, existing := range s.publishOperations {
		if !samePublishOperationScope(existing, candidate) {
			continue
		}
		if publishOperationExpired(existing, now) {
			delete(s.publishOperations, key)
			continue
		}
		if existing.PayloadDigest != candidate.PayloadDigest {
			return existing, false, ErrConflict
		}
		return existing, false, nil
	}
	s.publishOperations[tenantKey(candidate.TenantID, candidate.ID)] = candidate
	return candidate, true, nil
}

func (s *MemoryStore) SavePublishOperation(tenantID string, operation domain.PublishOperation) error {
	if tenantID == "" || operation.TenantID != tenantID {
		return errors.New("publish operation tenant identity mismatch")
	}
	if err := validatePublishOperation(operation); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishOperations[tenantKey(tenantID, operation.ID)] = operation
	return nil
}

func (s *MemoryStore) PublishOperation(tenantID, operationID string) (domain.PublishOperation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	operation, ok := s.publishOperations[tenantKey(tenantID, operationID)]
	return operation, ok
}

func (s *FileStore) ReservePublishOperation(candidate domain.PublishOperation, now time.Time) (domain.PublishOperation, bool, error) {
	if err := validatePublishOperation(candidate); err != nil {
		return domain.PublishOperation{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next, err := cloneFileState(s.state)
	if err != nil {
		return domain.PublishOperation{}, false, err
	}
	for key, existing := range next.PublishOperations {
		if !samePublishOperationScope(existing, candidate) {
			continue
		}
		if publishOperationExpired(existing, now) {
			delete(next.PublishOperations, key)
			continue
		}
		if existing.PayloadDigest != candidate.PayloadDigest {
			return existing, false, ErrConflict
		}
		return existing, false, nil
	}
	next.PublishOperations[tenantKey(candidate.TenantID, candidate.ID)] = candidate
	if err := s.persistSnapshot(next); err != nil {
		return domain.PublishOperation{}, false, err
	}
	s.state = next
	return candidate, true, nil
}

func (s *FileStore) SavePublishOperation(tenantID string, operation domain.PublishOperation) error {
	if tenantID == "" || operation.TenantID != tenantID {
		return errors.New("publish operation tenant identity mismatch")
	}
	if err := validatePublishOperation(operation); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(func(next *fileState) error {
		next.PublishOperations[tenantKey(tenantID, operation.ID)] = operation
		return nil
	})
}

func (s *FileStore) PublishOperation(tenantID, operationID string) (domain.PublishOperation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	operation, ok := s.state.PublishOperations[tenantKey(tenantID, operationID)]
	return operation, ok
}
