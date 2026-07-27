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

// Store wraps the "user_routing" table (one row per sub_id) and the
// single-row "routing_generator" table.
type Store struct {
	db *sql.DB
}

// New builds a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Get returns subID's routing toggle/string. A missing row is not an
// error -- it just means routing has never been configured for this
// subscriber, reported as enabled=false with an empty string.
func (s *Store) Get(subID string) (enabled bool, routingB64 string, err error) {
	var enabledInt int
	err = s.db.QueryRow(`SELECT enabled, routing_b64 FROM user_routing WHERE sub_id = ?`, subID).Scan(&enabledInt, &routingB64)
	switch {
	case err == nil:
		return enabledInt != 0, routingB64, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, "", nil
	default:
		return false, "", fmt.Errorf("routing: query %s: %w", subID, err)
	}
}

// Set creates or overwrites subID's routing toggle/string.
func (s *Store) Set(subID string, enabled bool, routingB64 string) error {
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}

	_, err := s.db.Exec(`
		INSERT INTO user_routing (sub_id, enabled, routing_b64, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(sub_id) DO UPDATE SET enabled = excluded.enabled, routing_b64 = excluded.routing_b64, updated_at = excluded.updated_at`,
		subID, enabledInt, routingB64, time.Now().UnixNano())
	if err != nil {
		return fmt.Errorf("routing: set %s: %w", subID, err)
	}
	return nil
}

// DeleteAll removes subID's routing row, if any.
func (s *Store) DeleteAll(subID string) error {
	if _, err := s.db.Exec(`DELETE FROM user_routing WHERE sub_id = ?`, subID); err != nil {
		return fmt.Errorf("routing: delete %s: %w", subID, err)
	}
	return nil
}

// GetGenerator loads the Routing Generator page's persisted state -- the
// last-edited profile fields and the last Base64 blob they produced. A
// missing row (nothing generated yet) is not an error -- it's reported as
// zero values.
func (s *Store) GetGenerator() (name string, profile Profile, generatedB64 string, err error) {
	var profileJSON string
	err = s.db.QueryRow(`SELECT name, profile, generated_b64 FROM routing_generator WHERE id = 1`).
		Scan(&name, &profileJSON, &generatedB64)
	switch {
	case err == nil:
		if jsonErr := json.Unmarshal([]byte(profileJSON), &profile); jsonErr != nil {
			return "", Profile{}, "", fmt.Errorf("routing: decode generator profile: %w", jsonErr)
		}
		return name, profile, generatedB64, nil
	case errors.Is(err, sql.ErrNoRows):
		return "", Profile{}, "", nil
	default:
		return "", Profile{}, "", fmt.Errorf("routing: query generator: %w", err)
	}
}

// SaveGenerator encodes profile (named per name), persists both the raw
// inputs and the fresh encode as the Generator page's state, and returns
// that encode for immediate display.
func (s *Store) SaveGenerator(name string, profile Profile) (GeneratedRouting, error) {
	generated, err := profile.Encode(name)
	if err != nil {
		return GeneratedRouting{}, err
	}

	profileJSON, err := json.Marshal(profile)
	if err != nil {
		return GeneratedRouting{}, fmt.Errorf("routing: encode generator profile: %w", err)
	}

	_, err = s.db.Exec(`
		INSERT INTO routing_generator (id, name, profile, generated_b64, updated_at) VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, profile = excluded.profile, generated_b64 = excluded.generated_b64, updated_at = excluded.updated_at`,
		name, string(profileJSON), generated.Base64, time.Now().UnixNano())
	if err != nil {
		return GeneratedRouting{}, fmt.Errorf("routing: save generator: %w", err)
	}
	return generated, nil
}
