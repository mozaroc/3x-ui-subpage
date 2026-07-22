package apps

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE applications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			icon TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			platforms TEXT NOT NULL DEFAULT '[]',
			download TEXT NOT NULL DEFAULT '',
			deeplink TEXT NOT NULL DEFAULT '',
			instructions TEXT NOT NULL DEFAULT '',
			visible INTEGER NOT NULL DEFAULT 1,
			sort_order INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL
		)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func insertApp(t *testing.T, db *sql.DB, name, platforms string, visible int, sortOrder int, updatedAt int64, deeplink string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO applications (name, platforms, visible, sort_order, updated_at, deeplink) VALUES (?, ?, ?, ?, ?, ?)`,
		name, platforms, visible, sortOrder, updatedAt, deeplink)
	if err != nil {
		t.Fatalf("insert app %s: %v", name, err)
	}
}

func TestList_SortedAndVisibleOnly(t *testing.T) {
	db := openTestDB(t)
	insertApp(t, db, "B-App", `["Android"]`, 1, 2, 1, "b://{subscription}")
	insertApp(t, db, "A-App", `["iOS"]`, 1, 1, 1, "a://{subscription}")
	insertApp(t, db, "Hidden-App", `[]`, 0, 0, 1, "")

	c := New(db)
	apps, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(apps) != 2 {
		t.Fatalf("expected 2 visible apps, got %d: %+v", len(apps), apps)
	}
	if apps[0].Name != "A-App" || apps[1].Name != "B-App" {
		t.Errorf("expected sorted order A-App,B-App, got %s,%s", apps[0].Name, apps[1].Name)
	}
}

func TestFilterByPlatform_CaseInsensitive(t *testing.T) {
	db := openTestDB(t)
	insertApp(t, db, "B-App", `["Android"]`, 1, 2, 1, "")
	insertApp(t, db, "A-App", `["iOS"]`, 1, 1, 1, "")

	c := New(db)
	apps, err := c.FilterByPlatform("ios")
	if err != nil {
		t.Fatalf("FilterByPlatform: %v", err)
	}
	if len(apps) != 1 || apps[0].Name != "A-App" {
		t.Fatalf("expected [A-App], got %+v", apps)
	}
}

func TestList_HotReload(t *testing.T) {
	db := openTestDB(t)
	insertApp(t, db, "v1", `[]`, 1, 1, 1, "")

	c := New(db)
	apps, err := c.List()
	if err != nil {
		t.Fatalf("List (initial): %v", err)
	}
	if len(apps) != 1 || apps[0].Name != "v1" {
		t.Fatalf("expected [v1], got %+v", apps)
	}

	if _, err := db.Exec(`UPDATE applications SET name = 'v2', updated_at = 2 WHERE name = 'v1'`); err != nil {
		t.Fatalf("update app: %v", err)
	}

	apps, err = c.List()
	if err != nil {
		t.Fatalf("List (after edit): %v", err)
	}
	if len(apps) != 1 || apps[0].Name != "v2" {
		t.Fatalf("expected reloaded [v2], got %+v", apps)
	}
}

// TestDelete_InvalidatesCacheEvenWhenMaxUpdatedAtUnchanged guards against a
// real bug: deleting a row can only ever leave the remaining MAX(updated_at)
// the same or lower, never higher, so a staleness check based on
// MAX(updated_at) alone can't detect a delete unless the deleted row
// happened to be the single most-recently-touched one. Here the *older* of
// two apps is deleted, so the survivor's updated_at is already equal to
// what was cached as the max — a MAX-only check would wrongly consider the
// cache still fresh and keep serving the deleted app.
func TestDelete_InvalidatesCacheEvenWhenMaxUpdatedAtUnchanged(t *testing.T) {
	db := openTestDB(t)
	insertApp(t, db, "old", `[]`, 1, 1, 1, "")
	insertApp(t, db, "newer", `[]`, 1, 2, 2, "")

	c := New(db)
	apps, err := c.ListAll()
	if err != nil {
		t.Fatalf("ListAll (initial): %v", err)
	}
	if len(apps) != 2 {
		t.Fatalf("expected 2 apps, got %+v", apps)
	}

	var oldID int64
	for _, a := range apps {
		if a.Name == "old" {
			oldID = a.ID
		}
	}
	if err := c.Delete(oldID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	apps, err = c.ListAll()
	if err != nil {
		t.Fatalf("ListAll (after delete): %v", err)
	}
	if len(apps) != 1 || apps[0].Name != "newer" {
		t.Fatalf("expected deletion to be reflected immediately, got %+v", apps)
	}
}

func TestList_EmptyCatalog(t *testing.T) {
	db := openTestDB(t)
	c := New(db)

	apps, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(apps) != 0 {
		t.Fatalf("expected empty catalog, got %+v", apps)
	}
}

func TestCreate_GetUpdateDelete(t *testing.T) {
	db := openTestDB(t)
	c := New(db)

	id, err := c.Create(App{Name: "New App", Platforms: []string{"Android"}, Visible: true, SortOrder: 5})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	got, err := c.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "New App" || got.SortOrder != 5 {
		t.Errorf("unexpected app: %+v", got)
	}

	got.Name = "Renamed App"
	got.Visible = false
	if err := c.Update(id, got); err != nil {
		t.Fatalf("Update: %v", err)
	}

	updated, err := c.Get(id)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if updated.Name != "Renamed App" || updated.Visible {
		t.Errorf("update did not apply: %+v", updated)
	}

	if err := c.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := c.Get(id); err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestUpdate_NotFound(t *testing.T) {
	db := openTestDB(t)
	c := New(db)
	if err := c.Update(999, App{Name: "x"}); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDelete_NotFound(t *testing.T) {
	db := openTestDB(t)
	c := New(db)
	if err := c.Delete(999); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListAll_IncludesHidden(t *testing.T) {
	db := openTestDB(t)
	insertApp(t, db, "Hidden", `[]`, 0, 1, 1, "")
	insertApp(t, db, "Visible", `[]`, 1, 2, 1, "")

	c := New(db)
	all, err := c.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 apps (incl. hidden), got %d", len(all))
	}
}

func TestRenderDeeplink_SubstitutesPlaceholders(t *testing.T) {
	got := RenderDeeplink("clash://install-config?url={subscription}&name={profileTitle}", "https://sub.example.com/x", "MyProfile")
	want := "clash://install-config?url=https://sub.example.com/x&name=MyProfile"
	if got != want {
		t.Errorf("RenderDeeplink() = %q, want %q", got, want)
	}
}
