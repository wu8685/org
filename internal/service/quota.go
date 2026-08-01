package service

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/wu8685/org/internal/domain"
)

type QuotaExceededError struct{ Category string }

func (e *QuotaExceededError) Error() string { return "tenant_quota_exceeded: " + e.Category }
func (e *QuotaExceededError) Unwrap() error { return ErrTenantQuotaExceeded }

func checkQuota(policy domain.TenantQuotaPolicy, existing []domain.QuotaLease, candidate domain.QuotaLease) error {
	usage := candidate
	for _, lease := range existing {
		usage.ReservedCPUMilli += lease.ReservedCPUMilli
		usage.ReservedMemoryBytes += lease.ReservedMemoryBytes
		usage.ActiveWorkerPods += lease.ActiveWorkerPods
		usage.ActiveReleases += lease.ActiveReleases
		usage.ConcurrentRuns += lease.ConcurrentRuns
		usage.ConcurrentDeployments += lease.ConcurrentDeployments
	}
	maxCPU, err := parseCPU(policy.MaxReservedCPU)
	if err != nil {
		return fmt.Errorf("invalid tenant CPU quota: %w", err)
	}
	maxMemory, err := parseMemory(policy.MaxReservedMemory)
	if err != nil {
		return fmt.Errorf("invalid tenant memory quota: %w", err)
	}
	checks := []struct {
		category string
		used     int64
		limit    int64
	}{
		{"reserved_cpu", usage.ReservedCPUMilli, maxCPU},
		{"reserved_memory", usage.ReservedMemoryBytes, maxMemory},
		{"active_worker_pods", int64(usage.ActiveWorkerPods), int64(policy.MaxActiveWorkerPods)},
		{"active_releases", int64(usage.ActiveReleases), int64(policy.MaxActiveReleases)},
		{"concurrent_runs", int64(usage.ConcurrentRuns), int64(policy.MaxConcurrentRuns)},
		{"concurrent_deployments", int64(usage.ConcurrentDeployments), int64(policy.MaxConcurrentDeployments)},
	}
	for _, check := range checks {
		if check.used > check.limit {
			return &QuotaExceededError{Category: check.category}
		}
	}
	return nil
}

func parseCPU(value string) (int64, error) {
	if strings.HasSuffix(value, "m") {
		return strconv.ParseInt(strings.TrimSuffix(value, "m"), 10, 64)
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || number < 0 {
		return 0, errors.New("invalid CPU quantity")
	}
	return int64(number * 1000), nil
}

func parseMemory(value string) (int64, error) {
	multipliers := []struct {
		suffix string
		value  float64
	}{
		{"Pi", 1 << 50}, {"Ti", 1 << 40}, {"Gi", 1 << 30}, {"Mi", 1 << 20}, {"Ki", 1 << 10},
		{"P", 1e15}, {"T", 1e12}, {"G", 1e9}, {"M", 1e6}, {"K", 1e3},
	}
	multiplier := float64(1)
	numberText := value
	for _, candidate := range multipliers {
		if strings.HasSuffix(value, candidate.suffix) {
			multiplier = candidate.value
			numberText = strings.TrimSuffix(value, candidate.suffix)
			break
		}
	}
	number, err := strconv.ParseFloat(numberText, 64)
	if err != nil || number < 0 {
		return 0, errors.New("invalid memory quantity")
	}
	return int64(number * multiplier), nil
}

func (s *MemoryStore) AcquireQuotaLease(tenantID string, lease domain.QuotaLease) error {
	if tenantID == "" || lease.TenantID != tenantID || lease.ID == "" {
		return errors.New("quota lease tenant identity mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantKey(tenantID, lease.ID)
	if _, exists := s.quotaLeases[key]; exists {
		return nil
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
	s.quotaLeases[key] = lease
	return nil
}

func (s *MemoryStore) ReleaseQuotaLease(tenantID, leaseID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.quotaLeases, tenantKey(tenantID, leaseID))
	return nil
}

func (s *MemoryStore) QuotaLeases(tenantID string) []domain.QuotaLease {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.QuotaLease, 0)
	for _, lease := range s.quotaLeases {
		if lease.TenantID == tenantID {
			out = append(out, lease)
		}
	}
	return out
}

func (s *MemoryStore) ReconcileQuotaLeases(tenantID string, activeIDs map[string]bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for key, lease := range s.quotaLeases {
		if lease.TenantID == tenantID && !activeIDs[lease.ID] {
			delete(s.quotaLeases, key)
			removed++
		}
	}
	return removed, nil
}

func (s *FileStore) AcquireQuotaLease(tenantID string, lease domain.QuotaLease) error {
	if tenantID == "" || lease.TenantID != tenantID || lease.ID == "" {
		return errors.New("quota lease tenant identity mismatch")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := tenantKey(tenantID, lease.ID)
	if _, exists := s.state.QuotaLeases[key]; exists {
		return nil
	}
	tenant, ok := s.state.Tenants[tenantID]
	if !ok {
		return ErrPermissionDenied
	}
	existing := make([]domain.QuotaLease, 0)
	for _, item := range s.state.QuotaLeases {
		if item.TenantID == tenantID {
			existing = append(existing, item)
		}
	}
	if err := checkQuota(tenant.QuotaPolicy, existing, lease); err != nil {
		return err
	}
	s.state.QuotaLeases[key] = lease
	return s.persist()
}

func (s *FileStore) ReleaseQuotaLease(tenantID, leaseID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.state.QuotaLeases, tenantKey(tenantID, leaseID))
	return s.persist()
}

func (s *FileStore) QuotaLeases(tenantID string) []domain.QuotaLease {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]domain.QuotaLease, 0)
	for _, lease := range s.state.QuotaLeases {
		if lease.TenantID == tenantID {
			out = append(out, lease)
		}
	}
	return out
}

func (s *FileStore) ReconcileQuotaLeases(tenantID string, activeIDs map[string]bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for key, lease := range s.state.QuotaLeases {
		if lease.TenantID == tenantID && !activeIDs[lease.ID] {
			delete(s.state.QuotaLeases, key)
			removed++
		}
	}
	if removed == 0 {
		return 0, nil
	}
	return removed, s.persist()
}
