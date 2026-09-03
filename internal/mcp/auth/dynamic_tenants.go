package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// DefaultBearerTTL is how long an auto-issued bearer stays valid.
const DefaultBearerTTL = 24 * time.Hour

// bearerLen is the byte length of generated bearers (32 = 256-bit;
// encoded as 64 hex chars).
const bearerLen = 32

// ErrBearerNotFound — bearer was never issued or has expired and been
// swept.
var ErrBearerNotFound = errors.New("bearer not found")

// ErrBearerExpired — bearer was issued but its TTL elapsed.
var ErrBearerExpired = errors.New("bearer expired")

// TenantRecord is the dynamic-store view of an auto-issued tenant.
type TenantRecord struct {
	TenantID  string
	Owner     string
	ExpiresAt time.Time
}

// DynamicTenantStore mints + tracks self-service tenants. Concurrency-
// safe; TTL-bounded with background sweep.
type DynamicTenantStore struct {
	ttl      time.Duration
	now      func() time.Time
	mu       sync.RWMutex
	byBearer map[string]string       // bearer → tenant_id
	byTenant map[string]TenantRecord // tenant_id → record
}

// DynamicTenantStoreConfig captures the bearer lifetime.
type DynamicTenantStoreConfig struct {
	BearerTTL time.Duration
}

// NewDynamicTenantStore constructs a store from the supplied config.
// now defaults to time.Now when nil (test path injects a fake clock).
func NewDynamicTenantStore(cfg DynamicTenantStoreConfig, now func() time.Time) *DynamicTenantStore {
	if now == nil {
		now = time.Now
	}
	return &DynamicTenantStore{
		ttl:      cfg.BearerTTL,
		now:      now,
		byBearer: map[string]string{},
		byTenant: map[string]TenantRecord{},
	}
}

// Mint creates a fresh tenant for owner: generates a UUID tenant_id,
// generates a random bearer, stores both, returns (bearer, tenant_id,
// expires_at). If an active tenant already exists for owner, this still
// mints a new one — letting the agent re-auth without explicitly
// invalidating the previous bearer (the old one stays valid until its
// own TTL elapses; both work).
func (s *DynamicTenantStore) Mint(owner string) (bearer, tenantID string, expiresAt time.Time, err error) {
	if owner == "" {
		return "", "", time.Time{}, fmt.Errorf("owner is required to mint a tenant")
	}
	bbuf := make([]byte, bearerLen)
	if _, err := rand.Read(bbuf); err != nil {
		return "", "", time.Time{}, fmt.Errorf("generate bearer: %w", err)
	}
	tbuf := make([]byte, 8)
	if _, err := rand.Read(tbuf); err != nil {
		return "", "", time.Time{}, fmt.Errorf("generate tenant id: %w", err)
	}
	bearer = hex.EncodeToString(bbuf)
	tenantID = "auto-" + hex.EncodeToString(tbuf)
	expiresAt = s.now().Add(s.ttl).UTC()

	rec := TenantRecord{TenantID: tenantID, Owner: owner, ExpiresAt: expiresAt}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.byBearer[bearer] = tenantID
	s.byTenant[tenantID] = rec
	return bearer, tenantID, expiresAt, nil
}

// LookupByBearer returns the tenant record bound to bearer, or
// ErrBearerNotFound / ErrBearerExpired.
func (s *DynamicTenantStore) LookupByBearer(bearer string) (TenantRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	tenantID, ok := s.byBearer[bearer]
	if !ok {
		return TenantRecord{}, ErrBearerNotFound
	}
	rec, ok := s.byTenant[tenantID]
	if !ok {
		return TenantRecord{}, ErrBearerNotFound
	}
	if !s.now().Before(rec.ExpiresAt) {
		return TenantRecord{}, ErrBearerExpired
	}
	return rec, nil
}

// Sweep removes every expired entry across both maps. Driven by a
// background goroutine in the server; callable from tests for
// determinism.
func (s *DynamicTenantStore) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for bearer, tenantID := range s.byBearer {
		rec, ok := s.byTenant[tenantID]
		if !ok || !now.Before(rec.ExpiresAt) {
			delete(s.byBearer, bearer)
			delete(s.byTenant, tenantID)
		}
	}
}

// Len reports the live tenant count (test helper).
func (s *DynamicTenantStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byTenant)
}
