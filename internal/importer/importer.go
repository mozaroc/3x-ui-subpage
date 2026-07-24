// Package importer seeds the SQLite database from the on-disk web/ tree
// built in phase 1 (applications catalog, themes, generator templates) —
// the one-time migration path from file-based config to the DB-backed
// store. Re-running it (e.g. on every `install.sh --update`) refreshes
// applications and the matching theme slugs unconditionally, and adds any
// new template rows it finds on disk, but it never overwrites a template
// row an administrator has edited since the importer itself last wrote it
// — see upsertTemplatePreservingEdits. It also never touches data an
// administrator only added directly via SQL (e.g. hand-written profiles or
// assignments).
//
// Convention for adding template profiles beyond "default" without an
// admin UI: for Clash/Mihomo/the full Xray JSON config, the file's stem
// becomes the profile name (e.g. "gaming.yaml.tmpl" -> profile "gaming");
// for the per-protocol Xray link templates, put profile variants in a
// subdirectory named after the profile (e.g. "xray/gaming/vless.tmpl").
package importer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Import walks webDir (expected to look like the phase-1 "web/" tree:
// applications/apps.json, themes/<slug>/*, templates/{xray,clash,mihomo}/*)
// and seeds the corresponding database rows.
func Import(db *sql.DB, webDir string) error {
	now := time.Now().UnixNano()

	if err := importApplications(db, filepath.Join(webDir, "applications", "apps.json"), now); err != nil {
		return err
	}
	if err := importThemes(db, filepath.Join(webDir, "themes"), now); err != nil {
		return err
	}
	if err := importXrayTemplates(db, filepath.Join(webDir, "templates", "xray"), now); err != nil {
		return err
	}
	if err := importYAMLTemplates(db, filepath.Join(webDir, "templates", "clash"), "clash", now); err != nil {
		return err
	}
	if err := importYAMLTemplates(db, filepath.Join(webDir, "templates", "mihomo"), "mihomo", now); err != nil {
		return err
	}
	if err := importYAMLTemplates(db, filepath.Join(webDir, "templates", "happ"), "happ", now); err != nil {
		return err
	}
	if err := importYAMLTemplates(db, filepath.Join(webDir, "templates", "incy"), "incy", now); err != nil {
		return err
	}
	if err := importRoutingRules(db, filepath.Join(webDir, "routing", "rules.json"), now); err != nil {
		return err
	}

	return nil
}

type appJSON struct {
	Name         string   `json:"name"`
	Icon         string   `json:"icon"`
	Description  string   `json:"description"`
	Platforms    []string `json:"platforms"`
	Download     string   `json:"download"`
	Deeplink     string   `json:"deeplink"`
	Instructions string   `json:"instructions"`
	Visible      *bool    `json:"visible"`
	SortOrder    int      `json:"sortOrder"`
}

func importApplications(db *sql.DB, path string, now int64) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("importer: read %s: %w", path, err)
	}

	var file struct {
		Applications []appJSON `json:"applications"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("importer: parse %s: %w", path, err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("importer: begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM applications`); err != nil {
		return fmt.Errorf("importer: clear applications: %w", err)
	}

	stmt, err := tx.Prepare(`
		INSERT INTO applications (name, icon, description, platforms, download, deeplink, instructions, visible, sort_order, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("importer: prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, a := range file.Applications {
		visible := 1
		if a.Visible != nil && !*a.Visible {
			visible = 0
		}
		platforms, err := json.Marshal(a.Platforms)
		if err != nil {
			return fmt.Errorf("importer: marshal platforms for %q: %w", a.Name, err)
		}
		if _, err := stmt.Exec(a.Name, a.Icon, a.Description, string(platforms), a.Download, a.Deeplink, a.Instructions, visible, a.SortOrder, now); err != nil {
			return fmt.Errorf("importer: insert application %q: %w", a.Name, err)
		}
	}

	return tx.Commit()
}

type themeMetaJSON struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Logo        string            `json:"logo"`
	Favicon     string            `json:"favicon"`
	Colors      map[string]string `json:"colors"`
	Fonts       map[string]string `json:"fonts"`
}

func importThemes(db *sql.DB, themesDir string, now int64) error {
	entries, err := os.ReadDir(themesDir)
	if err != nil {
		return fmt.Errorf("importer: read themes dir %s: %w", themesDir, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		slug := entry.Name()
		if err := importOneTheme(db, filepath.Join(themesDir, slug), slug, now); err != nil {
			return fmt.Errorf("importer: theme %q: %w", slug, err)
		}
	}
	return nil
}

func importOneTheme(db *sql.DB, themeDir, slug string, now int64) error {
	metaPath := filepath.Join(themeDir, "theme.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("read theme.json: %w", err)
	}

	var meta themeMetaJSON
	if err := json.Unmarshal(data, &meta); err != nil {
		return fmt.Errorf("parse theme.json: %w", err)
	}

	colors, err := json.Marshal(meta.Colors)
	if err != nil {
		return fmt.Errorf("marshal colors: %w", err)
	}
	fonts, err := json.Marshal(meta.Fonts)
	if err != nil {
		return fmt.Errorf("marshal fonts: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO themes (slug, name, description, logo, favicon, colors, fonts, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(slug) DO UPDATE SET
			name = excluded.name, description = excluded.description,
			logo = excluded.logo, favicon = excluded.favicon,
			colors = excluded.colors, fonts = excluded.fonts, updated_at = excluded.updated_at`,
		slug, meta.Name, meta.Description, meta.Logo, meta.Favicon, string(colors), string(fonts), now)
	if err != nil {
		return fmt.Errorf("upsert themes row: %w", err)
	}

	if _, err := tx.Exec(`DELETE FROM theme_files WHERE theme_slug = ?`, slug); err != nil {
		return fmt.Errorf("clear theme_files: %w", err)
	}

	stmt, err := tx.Prepare(`INSERT INTO theme_files (theme_slug, path, content, updated_at) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare theme_files insert: %w", err)
	}
	defer stmt.Close()

	err = filepath.WalkDir(themeDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(themeDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "theme.json" {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if _, err := stmt.Exec(slug, rel, content, now); err != nil {
			return fmt.Errorf("insert theme_files %s: %w", rel, err)
		}
		return nil
	})
	if err != nil {
		return err
	}

	return tx.Commit()
}

// manifestKey identifies one template row's slot; used to detect rows the
// importer itself previously wrote that have since disappeared from disk.
type manifestKey struct {
	Format   string
	Profile  string
	Protocol string
}

// loadManifest returns every import_manifest row for the given formats:
// what the importer itself wrote the *last* time it ran, and when.
func loadManifest(tx *sql.Tx, formats []string) (map[manifestKey]int64, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(formats)), ",")
	args := make([]any, len(formats))
	for i, f := range formats {
		args[i] = f
	}
	rows, err := tx.Query(`SELECT format, profile, protocol, updated_at FROM import_manifest WHERE format IN (`+placeholders+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("importer: load manifest: %w", err)
	}
	defer rows.Close()

	out := make(map[manifestKey]int64)
	for rows.Next() {
		var k manifestKey
		var updatedAt int64
		if err := rows.Scan(&k.Format, &k.Profile, &k.Protocol, &updatedAt); err != nil {
			return nil, fmt.Errorf("importer: scan manifest row: %w", err)
		}
		out[k] = updatedAt
	}
	return out, rows.Err()
}

// recordManifest upserts current into import_manifest, marking it as
// written by this import run at time now.
func recordManifest(tx *sql.Tx, k manifestKey, now int64) error {
	_, err := tx.Exec(`
		INSERT INTO import_manifest (format, profile, protocol, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(format, profile, protocol) DO UPDATE SET updated_at = excluded.updated_at`,
		k.Format, k.Profile, k.Protocol, now)
	return err
}

// upsertTemplatePreservingEdits writes newContent for k, but never
// overwrites a row an administrator has customized since the importer
// itself last wrote it — preserving admin edits across a re-import (e.g.
// `install.sh --update`, or re-running `-import` by hand) is the whole
// point; only content the importer itself owns and hasn't been touched
// since is safe to refresh to a newer shipped default.
//
// A row with no prior manifest entry at all — a pre-existing DB seeded by
// an older binary version that didn't track provenance yet, or any other
// unknown-provenance row — is treated the same conservative way: assume it
// might be customized and leave it alone, permanently (it can never regain
// "pristine" status without a manifest entry, so it's simply never
// auto-refreshed again). An admin who genuinely wants a row reset to the
// shipped default can already do that from the Templates page (Delete,
// then re-import) — that goes through the fresh-insert path below, which
// does start tracking it.
func upsertTemplatePreservingEdits(tx *sql.Tx, k manifestKey, newContent string, oldManifest map[manifestKey]int64, current map[manifestKey]bool, now int64) error {
	current[k] = true

	var existingContent string
	var existingUpdatedAt int64
	err := tx.QueryRow(`SELECT content, updated_at FROM templates WHERE format = ? AND profile = ? AND protocol = ?`,
		k.Format, k.Profile, k.Protocol).Scan(&existingContent, &existingUpdatedAt)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.Exec(`INSERT INTO templates (format, profile, protocol, content, updated_at) VALUES (?, ?, ?, ?, ?)`,
			k.Format, k.Profile, k.Protocol, newContent, now); err != nil {
			return fmt.Errorf("importer: insert %+v: %w", k, err)
		}
		return recordManifest(tx, k, now)
	case err != nil:
		return fmt.Errorf("importer: check existing %+v: %w", k, err)
	}

	lastImportedAt, tracked := oldManifest[k]
	pristine := tracked && existingUpdatedAt == lastImportedAt
	if !pristine {
		// Admin-customized (or provenance unknown): never overwrite, and —
		// crucially — never touch the manifest either. Writing a manifest
		// entry that merely mirrors this row's current timestamp (without
		// us having just written that timestamp ourselves) would make the
		// *next* run see manifest == row and mistake it for untouched-
		// since-import, overwriting it then. Leaving the manifest exactly
		// as it was (at whatever it last legitimately reflected, or absent
		// entirely) means this key keeps failing the pristine check
		// forever, for as long as nothing here writes to it again —
		// manifest.updated_at must only ever be set in the same operation
		// that writes that same timestamp into the row itself (see the
		// two branches below and the insert case above).
		return nil
	}
	if existingContent == newContent {
		return nil
	}
	if _, err := tx.Exec(`UPDATE templates SET content = ?, updated_at = ? WHERE format = ? AND profile = ? AND protocol = ?`,
		newContent, now, k.Format, k.Profile, k.Protocol); err != nil {
		return fmt.Errorf("importer: refresh %+v: %w", k, err)
	}
	return recordManifest(tx, k, now)
}

// pruneOrphaned deletes templates rows the importer previously wrote (per
// old, loaded before this run touched anything) whose source file is no
// longer present this run (absent from current) — but only if the row's
// updated_at still matches what the importer itself last set it to, i.e. no
// admin edit has happened via the UI since. An admin-edited row is left
// alone (and simply drops out of tracking) even if its source file is later
// removed, since it's no longer purely import-owned content.
func pruneOrphaned(tx *sql.Tx, old map[manifestKey]int64, current map[manifestKey]bool) error {
	for k, lastImportedAt := range old {
		if current[k] {
			continue
		}
		var stillMatches bool
		var curUpdatedAt int64
		err := tx.QueryRow(`SELECT updated_at FROM templates WHERE format = ? AND profile = ? AND protocol = ?`,
			k.Format, k.Profile, k.Protocol).Scan(&curUpdatedAt)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// already gone
		case err != nil:
			return fmt.Errorf("importer: check orphaned %+v: %w", k, err)
		default:
			stillMatches = curUpdatedAt == lastImportedAt
		}

		if stillMatches {
			if _, err := tx.Exec(`DELETE FROM templates WHERE format = ? AND profile = ? AND protocol = ?`,
				k.Format, k.Profile, k.Protocol); err != nil {
				return fmt.Errorf("importer: prune orphaned %+v: %w", k, err)
			}
		}
		if _, err := tx.Exec(`DELETE FROM import_manifest WHERE format = ? AND profile = ? AND protocol = ?`,
			k.Format, k.Profile, k.Protocol); err != nil {
			return fmt.Errorf("importer: clear manifest %+v: %w", k, err)
		}
	}
	return nil
}

func importXrayTemplates(db *sql.DB, dir string, now int64) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("importer: read %s: %w", dir, err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("importer: begin tx: %w", err)
	}
	defer tx.Rollback()

	oldManifest, err := loadManifest(tx, []string{"xray_link", "xray_json"})
	if err != nil {
		return err
	}
	current := make(map[manifestKey]bool)

	for _, e := range entries {
		name := e.Name()

		switch {
		case e.IsDir():
			// Subdirectory = additional profile: <profile>/<protocol>.tmpl
			profile := name
			protoEntries, err := os.ReadDir(filepath.Join(dir, name))
			if err != nil {
				return fmt.Errorf("importer: read profile dir %s: %w", name, err)
			}
			for _, pe := range protoEntries {
				if pe.IsDir() || !strings.HasSuffix(pe.Name(), ".tmpl") {
					continue
				}
				protocol := strings.TrimSuffix(pe.Name(), ".tmpl")
				content, err := os.ReadFile(filepath.Join(dir, name, pe.Name()))
				if err != nil {
					return fmt.Errorf("importer: read %s/%s: %w", name, pe.Name(), err)
				}
				k := manifestKey{"xray_link", profile, protocol}
				if err := upsertTemplatePreservingEdits(tx, k, string(content), oldManifest, current, now); err != nil {
					return fmt.Errorf("importer: xray_link %s/%s: %w", profile, protocol, err)
				}
			}

		case name == "full-config.json.tmpl":
			content, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return fmt.Errorf("importer: read %s: %w", name, err)
			}
			k := manifestKey{"xray_json", "default", ""}
			if err := upsertTemplatePreservingEdits(tx, k, string(content), oldManifest, current, now); err != nil {
				return fmt.Errorf("importer: xray_json: %w", err)
			}

		case strings.HasSuffix(name, ".tmpl"):
			protocol := strings.TrimSuffix(name, ".tmpl")
			content, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return fmt.Errorf("importer: read %s: %w", name, err)
			}
			k := manifestKey{"xray_link", "default", protocol}
			if err := upsertTemplatePreservingEdits(tx, k, string(content), oldManifest, current, now); err != nil {
				return fmt.Errorf("importer: xray_link default/%s: %w", protocol, err)
			}
		}
	}

	if err := pruneOrphaned(tx, oldManifest, current); err != nil {
		return err
	}

	return tx.Commit()
}

type routingRuleJSON struct {
	Profile   string `json:"profile"`
	SortOrder int    `json:"sortOrder"`
	Type      string `json:"type"`
	Value     string `json:"value"`
	Outbound  string `json:"outbound"`
	Enabled   *bool  `json:"enabled"`
}

// importRoutingRules seeds routing_rules from a JSON array at path,
// clearing existing rows first (same idempotent-reimport behavior as
// importApplications) so re-running -import against the same web/ tree is
// safe to use for refreshing seed content.
func importRoutingRules(db *sql.DB, path string, now int64) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("importer: read %s: %w", path, err)
	}

	var rules []routingRuleJSON
	if err := json.Unmarshal(data, &rules); err != nil {
		return fmt.Errorf("importer: parse %s: %w", path, err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("importer: begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM routing_rules`); err != nil {
		return fmt.Errorf("importer: clear routing_rules: %w", err)
	}

	stmt, err := tx.Prepare(`
		INSERT INTO routing_rules (profile, sort_order, type, value, outbound, enabled, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("importer: prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, r := range rules {
		enabled := 1
		if r.Enabled != nil && !*r.Enabled {
			enabled = 0
		}
		if _, err := stmt.Exec(r.Profile, r.SortOrder, r.Type, r.Value, r.Outbound, enabled, now); err != nil {
			return fmt.Errorf("importer: insert routing rule %q/%q: %w", r.Profile, r.Type, err)
		}
	}

	return tx.Commit()
}

func importYAMLTemplates(db *sql.DB, dir, format string, now int64) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("importer: read %s: %w", dir, err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("importer: begin tx: %w", err)
	}
	defer tx.Rollback()

	oldManifest, err := loadManifest(tx, []string{format})
	if err != nil {
		return err
	}
	current := make(map[manifestKey]bool)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tmpl") {
			continue
		}
		profile := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".tmpl"), ".yaml")

		content, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("importer: read %s: %w", e.Name(), err)
		}
		k := manifestKey{format, profile, ""}
		if err := upsertTemplatePreservingEdits(tx, k, string(content), oldManifest, current, now); err != nil {
			return fmt.Errorf("importer: %s/%s: %w", format, profile, err)
		}
	}

	if err := pruneOrphaned(tx, oldManifest, current); err != nil {
		return err
	}

	return tx.Commit()
}
