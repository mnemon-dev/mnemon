package memory

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

func TestRecallReadOnlyPreservesSnapshot(t *testing.T) {
	oldDataDir, oldStoreName, oldReadOnly := dataDir, storeName, readOnly
	oldBasic, oldBrief, oldVerbose, oldLimit := recBasic, recBrief, recVerbose, recLimit
	oldCategory, oldSource, oldIntent := recCategory, recSource, recIntent
	t.Cleanup(func() {
		dataDir, storeName, readOnly = oldDataDir, oldStoreName, oldReadOnly
		recBasic, recBrief, recVerbose, recLimit = oldBasic, oldBrief, oldVerbose, oldLimit
		recCategory, recSource, recIntent = oldCategory, oldSource, oldIntent
	})
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("MNEMON_EMBED_ENDPOINT", "http://127.0.0.1:1")
	storeName, readOnly = "readonly-audit", true
	recBrief, recVerbose = false, false
	recCategory, recSource, recIntent = "", "", ""

	for _, dir := range []string{filepath.Join(root, "absolute data # 中文 %"), "relative data # 中文 %"} {
		t.Run(filepath.Base(dir), func(t *testing.T) {
			dataDir = dir
			db, err := store.Open(store.StoreDir(dataDir, storeName))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			insertTestInsight(t, db, "readonly-seed", "SQLite snapshot memory", "user", "2026-01-01T00:00:00Z")
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			path := db.Path()
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			for _, tc := range []struct {
				name, query string
				basic       bool
				limit       int
			}{
				{"metadata-sample", "", true, 6},
				{"keyword", "SQLite", true, 10},
				{"full-browse", "", true, 100000},
				{"smart", "SQLite", false, 10},
			} {
				t.Run(tc.name, func(t *testing.T) {
					recBasic, recLimit = tc.basic, tc.limit
					var runErr error
					output := captureStdout(t, func() { runErr = recallCmd.RunE(recallCmd, []string{tc.query}) })
					if runErr != nil {
						t.Fatalf("readonly recall: %v", runErr)
					}
					var results []model.Insight
					var decodeErr error
					if tc.basic {
						decodeErr = json.Unmarshal([]byte(output), &results)
					} else {
						var response struct{ Results []model.Insight }
						decodeErr = json.Unmarshal([]byte(output), &response)
						results = response.Results
					}
					if decodeErr != nil {
						t.Fatalf("decode recall: %v", decodeErr)
					}
					if len(results) != 1 || results[0].ID != "readonly-seed" {
						t.Fatalf("unexpected recall results: %s", output)
					}
				})
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("readonly recall changed database bytes, including access counters or oplog")
			}
			for _, suffix := range []string{"-wal", "-shm", "-journal"} {
				if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
					t.Fatalf("readonly recall created sidecar %s: %v", suffix, err)
				}
			}
		})
	}
}
