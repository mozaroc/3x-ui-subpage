// Package routing implements per-subscriber Happ/Incy "Routing Profile"
// configuration — the client apps' own native traffic-splitting feature
// (GlobalProxy/RouteOrder/DomainStrategy/DNS settings/Direct-Proxy-Block
// site+IP lists/FakeDNS/UseChunkFiles), documented at routing.happ.su and
// docs.incy.cc/en/routing. This is unrelated to Xray-core routing rules
// (GEOIP/domain/CIDR baked into a generated config body) — it's delivered
// to the app via the Routing/Routing-Enable HTTP response headers on the
// subscription request, mirroring upstream 3x-ui's own per-panel
// implementation but scoped per-subscriber here. See happjson.go for the
// wire encoding.
package routing

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Profile is one subscriber's Happ/Incy Routing Profile configuration.
type Profile struct {
	GlobalProxy    bool
	RouteOrder     string // one of the 6 Block/Proxy/Direct permutations
	DomainStrategy string // AsIs / IPIfNonMatch / IPOnDemand

	RemoteDNSType   string // DoH / DoU
	RemoteDNSDomain string
	RemoteDNSIP     string

	DomesticDNSType   string // DoH / DoU
	DomesticDNSDomain string
	DomesticDNSIP     string

	DNSHosts map[string]string

	GeoIPURL   string
	GeoSiteURL string

	DirectSites []string
	DirectIP    []string
	ProxySites  []string
	ProxyIP     []string
	BlockSites  []string
	BlockIP     []string

	FakeDNS       bool
	UseChunkFiles bool
}

// Store wraps the "user_routing" table (one row per sub_id).
type Store struct {
	db *sql.DB
}

// New builds a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Get returns subID's routing profile. A missing row is not an error --
// it just means routing has never been configured for this subscriber,
// reported as enabled=false with a zero-value Profile.
func (s *Store) Get(subID string) (enabled bool, profile Profile, err error) {
	var enabledInt int
	var config string
	err = s.db.QueryRow(`SELECT enabled, config FROM user_routing WHERE sub_id = ?`, subID).Scan(&enabledInt, &config)
	switch {
	case err == nil:
		if jsonErr := json.Unmarshal([]byte(config), &profile); jsonErr != nil {
			return false, Profile{}, fmt.Errorf("routing: decode profile for %s: %w", subID, jsonErr)
		}
		return enabledInt != 0, profile, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, Profile{}, nil
	default:
		return false, Profile{}, fmt.Errorf("routing: query %s: %w", subID, err)
	}
}

// Set creates or overwrites subID's routing profile.
func (s *Store) Set(subID string, enabled bool, profile Profile) error {
	config, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("routing: encode profile for %s: %w", subID, err)
	}

	enabledInt := 0
	if enabled {
		enabledInt = 1
	}

	_, err = s.db.Exec(`
		INSERT INTO user_routing (sub_id, enabled, config, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(sub_id) DO UPDATE SET enabled = excluded.enabled, config = excluded.config, updated_at = excluded.updated_at`,
		subID, enabledInt, string(config), time.Now().UnixNano())
	if err != nil {
		return fmt.Errorf("routing: set %s: %w", subID, err)
	}
	return nil
}

// DeleteAll removes subID's routing profile row, if any.
func (s *Store) DeleteAll(subID string) error {
	if _, err := s.db.Exec(`DELETE FROM user_routing WHERE sub_id = ?`, subID); err != nil {
		return fmt.Errorf("routing: delete %s: %w", subID, err)
	}
	return nil
}
