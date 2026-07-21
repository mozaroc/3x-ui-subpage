// Package importer seeds the SQLite database from the on-disk web/ tree
// built in phase 1 (applications catalog, themes, generator templates) —
// the one-time migration path from file-based config to the DB-backed
// store. Re-running it against the same directory overwrites what it
// manages (applications, the matching theme slugs, the matching template
// rows) so it's safe to use to refresh seed content, but it never touches
// data an administrator only added directly via SQL (e.g. hand-written
// profiles or assignments).
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

	upsert, err := tx.Prepare(`
		INSERT INTO templates (format, profile, protocol, content, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(format, profile, protocol) DO UPDATE SET content = excluded.content, updated_at = excluded.updated_at`)
	if err != nil {
		return fmt.Errorf("importer: prepare upsert: %w", err)
	}
	defer upsert.Close()

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
				if _, err := upsert.Exec("xray_link", profile, protocol, string(content), now); err != nil {
					return fmt.Errorf("importer: insert xray_link %s/%s: %w", profile, protocol, err)
				}
			}

		case name == "full-config.json.tmpl":
			content, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return fmt.Errorf("importer: read %s: %w", name, err)
			}
			if _, err := upsert.Exec("xray_json", "default", "", string(content), now); err != nil {
				return fmt.Errorf("importer: insert xray_json: %w", err)
			}

		case strings.HasSuffix(name, ".tmpl"):
			protocol := strings.TrimSuffix(name, ".tmpl")
			content, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return fmt.Errorf("importer: read %s: %w", name, err)
			}
			if _, err := upsert.Exec("xray_link", "default", protocol, string(content), now); err != nil {
				return fmt.Errorf("importer: insert xray_link default/%s: %w", protocol, err)
			}
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

	upsert, err := tx.Prepare(`
		INSERT INTO templates (format, profile, protocol, content, updated_at)
		VALUES (?, ?, '', ?, ?)
		ON CONFLICT(format, profile, protocol) DO UPDATE SET content = excluded.content, updated_at = excluded.updated_at`)
	if err != nil {
		return fmt.Errorf("importer: prepare upsert: %w", err)
	}
	defer upsert.Close()

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tmpl") {
			continue
		}
		profile := strings.TrimSuffix(strings.TrimSuffix(e.Name(), ".tmpl"), ".yaml")

		content, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return fmt.Errorf("importer: read %s: %w", e.Name(), err)
		}
		if _, err := upsert.Exec(format, profile, string(content), now); err != nil {
			return fmt.Errorf("importer: insert %s/%s: %w", format, profile, err)
		}
	}

	return tx.Commit()
}
