package users

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/irazin/3x-ui-subpage/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCreateGetUpdateDelete(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	id, err := s.Create(User{Username: "alice", SubID: "sub-alice", Enabled: true, TotalGB: 1000, ExpiryMs: 12345})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Username != "alice" || got.SubID != "sub-alice" {
		t.Fatalf("unexpected user: %+v", got)
	}
	if got.UUID == "" || got.Password == "" {
		t.Fatal("expected generated uuid/password")
	}
	if got.Method != defaultMethod {
		t.Fatalf("expected default method, got %q", got.Method)
	}

	got.Username = "alice2"
	got.TotalGB = 2000
	if err := s.Update(id, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got2, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	if got2.Username != "alice2" || got2.TotalGB != 2000 {
		t.Fatalf("update didn't apply: %+v", got2)
	}
	// uuid/password must survive a plain Update.
	if got2.UUID != got.UUID || got2.Password != got.Password {
		t.Fatal("Update must not touch uuid/password")
	}

	if err := s.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestCreate_DuplicateUsernameOrSubID(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	if _, err := s.Create(User{Username: "alice", SubID: "sub-1"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(User{Username: "alice", SubID: "sub-2"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate for duplicate username, got %v", err)
	}
	if _, err := s.Create(User{Username: "bob", SubID: "sub-1"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate for duplicate sub_id, got %v", err)
	}
}

func TestSetEnabled(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	id, err := s.Create(User{Username: "alice", SubID: "sub-1", Enabled: true})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := s.SetEnabled(id, false); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}
	got, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Enabled {
		t.Fatal("expected disabled")
	}
}

func TestRegenerateCredentials(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	id, err := s.Create(User{Username: "alice", SubID: "sub-1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	after, err := s.RegenerateCredentials(id)
	if err != nil {
		t.Fatalf("RegenerateCredentials: %v", err)
	}
	if after.UUID == before.UUID || after.Password == before.Password {
		t.Fatalf("expected new credentials, got same: before=%+v after=%+v", before, after)
	}
}

func TestList_SearchFilterSortPagination(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	mustCreate := func(username, subID string, enabled bool) int64 {
		id, err := s.Create(User{Username: username, SubID: subID, Enabled: enabled})
		if err != nil {
			t.Fatalf("Create %s: %v", username, err)
		}
		return id
	}
	mustCreate("charlie", "sub-charlie", true)
	mustCreate("alice", "sub-alice", true)
	mustCreate("bob", "sub-bob", false)

	all, total, err := s.List(ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 3 || len(all) != 3 {
		t.Fatalf("expected 3 users, got %d/%d", len(all), total)
	}
	if all[0].Username != "alice" || all[1].Username != "bob" || all[2].Username != "charlie" {
		t.Fatalf("expected default username sort, got %v", []string{all[0].Username, all[1].Username, all[2].Username})
	}

	enabledOnly, total, err := s.List(ListFilter{Status: "enabled"})
	if err != nil {
		t.Fatalf("List enabled: %v", err)
	}
	if total != 2 || len(enabledOnly) != 2 {
		t.Fatalf("expected 2 enabled users, got %d/%d", len(enabledOnly), total)
	}

	searched, total, err := s.List(ListFilter{Query: "ali"})
	if err != nil {
		t.Fatalf("List query: %v", err)
	}
	if total != 1 || len(searched) != 1 || searched[0].Username != "alice" {
		t.Fatalf("expected only alice, got %+v", searched)
	}

	page, total, err := s.List(ListFilter{SortBy: "username", SortDir: "desc", Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("List paginated: %v", err)
	}
	if total != 3 || len(page) != 2 || page[0].Username != "charlie" || page[1].Username != "bob" {
		t.Fatalf("unexpected paginated/sorted page: %+v (total %d)", page, total)
	}
}

func TestSetInbounds_DiffAddsAndRemoves(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	id, err := s.Create(User{Username: "alice", SubID: "sub-1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	added, removed, err := s.SetInbounds(id, []Desired{
		{InboundID: 1, InboundTag: "vless-in", Protocol: "vless"},
		{InboundID: 2, InboundTag: "trojan-in", Protocol: "trojan"},
	})
	if err != nil {
		t.Fatalf("SetInbounds: %v", err)
	}
	if len(added) != 2 || len(removed) != 0 {
		t.Fatalf("expected 2 added, 0 removed, got %d/%d", len(added), len(removed))
	}

	current, err := s.Inbounds(id)
	if err != nil {
		t.Fatalf("Inbounds: %v", err)
	}
	if len(current) != 2 {
		t.Fatalf("expected 2 assignments, got %d", len(current))
	}

	// Reconcile down to just inbound 2, plus a brand new inbound 3.
	added, removed, err = s.SetInbounds(id, []Desired{
		{InboundID: 2, InboundTag: "trojan-in", Protocol: "trojan"},
		{InboundID: 3, InboundTag: "ss-in", Protocol: "shadowsocks"},
	})
	if err != nil {
		t.Fatalf("SetInbounds second call: %v", err)
	}
	if len(added) != 1 || added[0].InboundID != 3 {
		t.Fatalf("expected only inbound 3 added, got %+v", added)
	}
	if len(removed) != 1 || removed[0].InboundID != 1 {
		t.Fatalf("expected only inbound 1 removed, got %+v", removed)
	}

	final, err := s.Inbounds(id)
	if err != nil {
		t.Fatalf("Inbounds: %v", err)
	}
	if len(final) != 2 {
		t.Fatalf("expected 2 final assignments, got %d", len(final))
	}
}

func TestDelete_CascadesUserInbounds(t *testing.T) {
	db := openTestDB(t)
	s := New(db)

	id, err := s.Create(User{Username: "alice", SubID: "sub-1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, _, err := s.SetInbounds(id, []Desired{{InboundID: 1, InboundTag: "in", Protocol: "vless"}}); err != nil {
		t.Fatalf("SetInbounds: %v", err)
	}

	if err := s.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_inbounds WHERE user_id = ?`, id).Scan(&count); err != nil {
		t.Fatalf("count user_inbounds: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected cascade delete of user_inbounds, got %d remaining", count)
	}
}
