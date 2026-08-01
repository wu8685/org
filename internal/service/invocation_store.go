package service

import (
	"errors"

	"github.com/wu8685/org/internal/domain"
)

func validateInvocationReservation(tenantID string, invocation domain.Invocation, lease domain.QuotaLease) error {
	if tenantID == "" || invocation.ID == "" || invocation.TenantID != tenantID || invocation.State != domain.InvocationStarting {
		return errors.New("invocation reservation tenant identity or state mismatch")
	}
	if lease.ID != "run:"+invocation.ID || lease.TenantID != tenantID || lease.Kind != domain.QuotaLeaseRun || lease.ConcurrentRuns != 1 {
		return errors.New("invocation quota reservation mismatch")
	}
	return nil
}

func (s *MemoryStore) CommitInvocationReservation(tenantID string, invocation domain.Invocation, lease domain.QuotaLease) error {
	if err := validateInvocationReservation(tenantID, invocation, lease); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	invocationKey, leaseKey := tenantKey(tenantID, invocation.ID), tenantKey(tenantID, lease.ID)
	if existing, ok := s.invocations[invocationKey]; ok {
		if existing.TemporalWorkflowID == invocation.TemporalWorkflowID && existing.SelectedVersion == invocation.SelectedVersion {
			return nil
		}
		return ErrConflict
	}
	tenant, ok := s.tenants[tenantID]
	if !ok {
		return ErrPermissionDenied
	}
	existing := make([]domain.QuotaLease, 0)
	for _, item := range s.quotaLeases {
		if item.TenantID == tenantID {
			existing = append(existing, item)
		}
	}
	if err := checkQuota(tenant.QuotaPolicy, existing, lease); err != nil {
		return err
	}
	s.invocations[invocationKey] = invocation
	s.quotaLeases[leaseKey] = lease
	return nil
}

func (s *FileStore) CommitInvocationReservation(tenantID string, invocation domain.Invocation, lease domain.QuotaLease) error {
	if err := validateInvocationReservation(tenantID, invocation, lease); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(func(next *fileState) error {
		invocationKey, leaseKey := tenantKey(tenantID, invocation.ID), tenantKey(tenantID, lease.ID)
		if existing, ok := next.Invocations[invocationKey]; ok {
			if existing.TemporalWorkflowID == invocation.TemporalWorkflowID && existing.SelectedVersion == invocation.SelectedVersion {
				return nil
			}
			return ErrConflict
		}
		tenant, ok := next.Tenants[tenantID]
		if !ok {
			return ErrPermissionDenied
		}
		existing := make([]domain.QuotaLease, 0)
		for _, item := range next.QuotaLeases {
			if item.TenantID == tenantID {
				existing = append(existing, item)
			}
		}
		if err := checkQuota(tenant.QuotaPolicy, existing, lease); err != nil {
			return err
		}
		next.Invocations[invocationKey] = invocation
		next.InvocationRouting[invocationKey] = captureInvocationRouting(invocation)
		next.QuotaLeases[leaseKey] = lease
		return nil
	})
}

func (s *MemoryStore) AllInvocations() []domain.Invocation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Invocation, 0, len(s.invocations))
	for _, invocation := range s.invocations {
		out = append(out, invocation)
	}
	return out
}

func (s *FileStore) AllInvocations() []domain.Invocation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.Invocation, 0, len(s.state.Invocations))
	for _, invocation := range s.state.Invocations {
		out = append(out, invocation)
	}
	return out
}

func validateInvocationTerminal(tenantID string, invocation domain.Invocation, leaseID string) error {
	if tenantID == "" || invocation.ID == "" || invocation.TenantID != tenantID || leaseID != "run:"+invocation.ID {
		return errors.New("terminal invocation tenant identity mismatch")
	}
	switch invocation.State {
	case domain.InvocationCompleted, domain.InvocationFailed, domain.InvocationCanceled:
		return nil
	default:
		return errors.New("terminal invocation state is required")
	}
}

func (s *MemoryStore) CommitInvocationTerminal(tenantID string, invocation domain.Invocation, leaseID string) error {
	if err := validateInvocationTerminal(tenantID, invocation, leaseID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantKey(tenantID, invocation.ID)
	if _, ok := s.invocations[key]; !ok {
		return ErrNotFound
	}
	s.invocations[key] = invocation
	delete(s.quotaLeases, tenantKey(tenantID, leaseID))
	return nil
}

func (s *FileStore) CommitInvocationTerminal(tenantID string, invocation domain.Invocation, leaseID string) error {
	if err := validateInvocationTerminal(tenantID, invocation, leaseID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutate(func(next *fileState) error {
		key := tenantKey(tenantID, invocation.ID)
		if _, ok := next.Invocations[key]; !ok {
			return ErrNotFound
		}
		next.Invocations[key] = invocation
		next.InvocationRouting[key] = captureInvocationRouting(invocation)
		delete(next.QuotaLeases, tenantKey(tenantID, leaseID))
		return nil
	})
}
