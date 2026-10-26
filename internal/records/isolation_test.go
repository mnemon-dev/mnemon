package records_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	legacy "github.com/mnemon-dev/mnemon/internal/memory/store"
	"github.com/mnemon-dev/mnemon/internal/records"
)

func legacySchema(t *testing.T, path string) []string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version string
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query("SELECT type||':'||name||':'||coalesce(sql,'') FROM sqlite_master ORDER BY type,name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []string{version}
	for rows.Next() {
		var text string
		if err = rows.Scan(&text); err != nil {
			t.Fatal(err)
		}
		out = append(out, text)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExistingMemoryDatabaseUnchanged(t *testing.T) {
	dir := t.TempDir()
	// This is the unmodified master schema: records has no migration or open path
	// into this file, including when both databases live in the same store.
	old, err := legacy.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := old.Path()
	old.Close()
	before := legacySchema(t, path)
	bytesBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := records.Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(context.Background(), records.GetRequest{}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if !reflect.DeepEqual(before, legacySchema(t, path)) {
		t.Fatal("legacy schema or user_version changed")
	}
	bytesAfter, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bytesBefore, bytesAfter) {
		t.Fatal("legacy database bytes changed")
	}
	if _, err = os.Stat(filepath.Join(dir, "records.db")); err != nil {
		t.Fatal(err)
	}
}
