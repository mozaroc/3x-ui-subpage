package theme

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestAdminStore_UpsertAndGetMeta(t *testing.T) {
	db := openTestDB(t)
	a := NewAdminStore(db)

	meta := Meta{Name: "New Theme", Colors: map[string]string{"primary": "#fff"}, Fonts: map[string]string{"body": "sans-serif"}}
	if err := a.UpsertMeta("newtheme", meta); err != nil {
		t.Fatalf("UpsertMeta: %v", err)
	}

	got, err := a.GetMeta("newtheme")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got.Name != "New Theme" || got.Colors["primary"] != "#fff" {
		t.Errorf("unexpected meta: %+v", got)
	}
}

func TestAdminStore_UpsertMetaOverwrites(t *testing.T) {
	db := openTestDB(t)
	a := NewAdminStore(db)

	if err := a.UpsertMeta("t", Meta{Name: "v1"}); err != nil {
		t.Fatalf("UpsertMeta v1: %v", err)
	}
	if err := a.UpsertMeta("t", Meta{Name: "v2"}); err != nil {
		t.Fatalf("UpsertMeta v2: %v", err)
	}

	got, err := a.GetMeta("t")
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if got.Name != "v2" {
		t.Errorf("expected overwritten name v2, got %q", got.Name)
	}
}

func TestAdminStore_GetMetaNotFound(t *testing.T) {
	db := openTestDB(t)
	a := NewAdminStore(db)

	if _, err := a.GetMeta("does-not-exist"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestAdminStore_ListSlugs(t *testing.T) {
	db := openTestDB(t)
	a := NewAdminStore(db)

	a.UpsertMeta("b", Meta{Name: "B"})
	a.UpsertMeta("a", Meta{Name: "A"})

	slugs, err := a.ListSlugs()
	if err != nil {
		t.Fatalf("ListSlugs: %v", err)
	}
	if len(slugs) != 2 || slugs[0] != "a" || slugs[1] != "b" {
		t.Errorf("expected sorted [a b], got %v", slugs)
	}
}

func TestAdminStore_FileCRUD(t *testing.T) {
	db := openTestDB(t)
	a := NewAdminStore(db)
	a.UpsertMeta("t", Meta{Name: "T"})

	if err := a.PutFile("t", "layout.html", []byte("<html></html>")); err != nil {
		t.Fatalf("PutFile: %v", err)
	}

	content, err := a.GetFile("t", "layout.html")
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if string(content) != "<html></html>" {
		t.Errorf("unexpected content: %q", content)
	}

	files, err := a.ListFiles("t")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(files) != 1 || files[0] != "layout.html" {
		t.Errorf("expected [layout.html], got %v", files)
	}

	if err := a.PutFile("t", "layout.html", []byte("<html>updated</html>")); err != nil {
		t.Fatalf("PutFile overwrite: %v", err)
	}
	content, _ = a.GetFile("t", "layout.html")
	if string(content) != "<html>updated</html>" {
		t.Errorf("expected overwritten content, got %q", content)
	}

	if err := a.DeleteFile("t", "layout.html"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if _, err := a.GetFile("t", "layout.html"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestAdminStore_DeleteFileNotFound(t *testing.T) {
	db := openTestDB(t)
	a := NewAdminStore(db)

	if err := a.DeleteFile("t", "nope.html"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestAdminStore_EditThenEngineRendersIt(t *testing.T) {
	db := openTestDB(t)
	a := NewAdminStore(db)

	if err := a.UpsertMeta("t", Meta{Name: "T"}); err != nil {
		t.Fatalf("UpsertMeta: %v", err)
	}
	if err := a.PutFile("t", "layout.html", []byte(`{{define "layout"}}{{template "content" .}}{{end}}`)); err != nil {
		t.Fatalf("PutFile layout: %v", err)
	}
	if err := a.PutFile("t", "pages/subscription.html", []byte(`{{define "content"}}via-admin-store{{end}}`)); err != nil {
		t.Fatalf("PutFile content: %v", err)
	}

	e := New(db, "t")
	var buf bytes.Buffer
	if err := e.Render(&buf, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(buf.String(), "via-admin-store") {
		t.Errorf("expected engine to pick up AdminStore-written files, got: %s", buf.String())
	}
}
