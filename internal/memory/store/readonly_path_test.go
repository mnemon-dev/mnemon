package store

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOpenReadOnlyPathForms(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	cases := []struct{ name, dir string }{
		{"absolute", filepath.Join(root, "absolute store # 中文 %")},
		{"relative", "relative store # 中文 %"},
	}
	if runtime.GOOS == "windows" {
		cases = append(cases, struct{ name, dir string }{
			"drive-relative", filepath.VolumeName(root) + "drive-relative store # 中文 %",
		})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			absoluteDir, err := filepath.Abs(tc.dir)
			if err != nil {
				t.Fatal(err)
			}
			db, err := Open(absoluteDir)
			if err != nil {
				t.Fatalf("create store: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := db.InsertInsight(makeInsight("readonly-seed", "snapshot memory", 2)); err != nil {
				t.Fatalf("insert seed: %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(absoluteDir, "mnemon.db")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			ro, err := OpenReadOnly(tc.dir)
			if err != nil {
				t.Fatalf("open readonly: %v", err)
			}
			t.Cleanup(func() { _ = ro.Close() })
			insights, err := ro.QueryInsights(QueryFilter{Keyword: "snapshot", Limit: 10})
			if err != nil {
				t.Fatalf("query readonly store: %v", err)
			}
			if len(insights) != 1 || insights[0].ID != "readonly-seed" {
				t.Fatalf("unexpected readonly results: %+v", insights)
			}
			if err := ro.IncrementAccessCount("readonly-seed"); err != nil {
				t.Fatal(err)
			}
			ro.LogOp("recall:basic", "", "readonly probe")
			if _, err := ro.Conn().Exec("UPDATE insights SET access_count = 7"); err == nil {
				t.Fatal("readonly connection accepted a database mutation")
			}
			for _, suffix := range []string{"-wal", "-shm", "-journal"} {
				if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
					t.Fatalf("readonly access created sidecar %s: %v", suffix, err)
				}
			}
			if err := ro.Close(); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("readonly access changed database bytes")
			}
		})
	}
}
