package memory

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mnemon-dev/mnemon/internal/memory/model"
	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

func configureRememberTest(t *testing.T) {
	t.Helper()
	oldDataDir, oldStoreName, oldReadOnly := dataDir, storeName, readOnly
	oldCategory, oldImportance := remCategory, remImportance
	oldTags, oldSource, oldEntities := remTags, remSource, remEntities
	oldEntityMode, oldNoDiff := remEntityMode, remNoDiff
	t.Cleanup(func() {
		dataDir, storeName, readOnly = oldDataDir, oldStoreName, oldReadOnly
		remCategory, remImportance = oldCategory, oldImportance
		remTags, remSource, remEntities = oldTags, oldSource, oldEntities
		remEntityMode, remNoDiff = oldEntityMode, oldNoDiff
	})

	dataDir = t.TempDir()
	storeName = ""
	readOnly = false
	remCategory = "fact"
	remImportance = 3
	remTags = ""
	remSource = "user"
	remEntities = ""
	remEntityMode = "merge"
	remNoDiff = true
	t.Setenv("MNEMON_EMBED_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("MNEMON_MAX_INSIGHTS", "1")
	t.Setenv("MNEMON_AUTO_PRUNE_MIN_AGE", "0")
}

func seedOldPruneCandidate(t *testing.T, id string) {
	t.Helper()
	db, err := store.Open(store.StoreDir(dataDir, store.DefaultStoreName))
	if err != nil {
		t.Fatalf("open seed store: %v", err)
	}
	createdAt := time.Now().UTC().Add(-48 * time.Hour)
	err = db.InsertInsight(&model.Insight{
		ID:         id,
		Content:    "old retention candidate",
		Category:   model.CategoryFact,
		Importance: 1,
		Tags:       []string{},
		Entities:   []string{},
		Source:     "test",
		CreatedAt:  createdAt,
		UpdatedAt:  createdAt,
	})
	if err != nil {
		db.Close()
		t.Fatalf("insert seed: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}
}

func TestRememberRejectsExplicitCLIValuesThatAreServiceDefaults(t *testing.T) {
	tests := []struct {
		name      string
		configure func()
		wantError string
	}{
		{
			name:      "empty category",
			configure: func() { remCategory = "" },
			wantError: `invalid category ""`,
		},
		{
			name:      "zero importance",
			configure: func() { remImportance = 0 },
			wantError: "importance must be 1-5, got 0",
		},
		{
			name:      "empty entity mode",
			configure: func() { remEntityMode = "" },
			wantError: `invalid entity mode ""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configureRememberTest(t)
			tt.configure()

			err := rememberCmd.RunE(rememberCmd, []string{"must not persist"})
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("remember error = %v, want %q", err, tt.wantError)
			}
			if store.StoreExists(dataDir, store.DefaultStoreName) {
				t.Fatal("invalid CLI input opened a store before being rejected")
			}
		})
	}
}

func TestRememberReportsAutoPrunedIDsAndTrigger(t *testing.T) {
	configureRememberTest(t)
	seedOldPruneCandidate(t, "old-prune-target")

	var runErr error
	out := captureStdout(t, func() {
		runErr = rememberCmd.RunE(rememberCmd, []string{"new durable memory"})
	})
	if runErr != nil {
		t.Fatalf("remember: %v", runErr)
	}
	var result struct {
		ID            string   `json:"id"`
		AutoPruned    int      `json:"auto_pruned"`
		AutoPrunedIDs []string `json:"auto_pruned_ids"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode remember output: %v\n%s", err, out)
	}
	if result.AutoPruned != 1 || len(result.AutoPrunedIDs) != 1 || result.AutoPrunedIDs[0] != "old-prune-target" {
		t.Fatalf("auto-prune result = count %d ids %v", result.AutoPruned, result.AutoPrunedIDs)
	}

	db, err := store.Open(store.StoreDir(dataDir, store.DefaultStoreName))
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer db.Close()
	entries, err := db.GetOplog(20)
	if err != nil {
		t.Fatalf("get oplog: %v", err)
	}
	found := false
	for _, entry := range entries {
		if entry.Operation == "prune" && entry.InsightID == "old-prune-target" {
			found = true
			if !strings.Contains(entry.Detail, "trigger="+result.ID) {
				t.Errorf("auto-prune detail %q does not name trigger %s", entry.Detail, result.ID)
			}
		}
	}
	if !found {
		t.Fatal("auto-pruned id is missing from the oplog")
	}
}

func TestRememberRollsBackWhenAutoPruneAuditFails(t *testing.T) {
	configureRememberTest(t)
	seedOldPruneCandidate(t, "rollback-old")

	db, err := store.Open(store.StoreDir(dataDir, store.DefaultStoreName))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := db.Conn().Exec(`
		CREATE TRIGGER reject_remember_auto_prune_audit
		BEFORE INSERT ON oplog
		WHEN NEW.operation = 'prune'
		BEGIN
			SELECT RAISE(ABORT, 'audit unavailable');
		END`); err != nil {
		db.Close()
		t.Fatalf("create rejecting trigger: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	if err := rememberCmd.RunE(rememberCmd, []string{"must roll back"}); err == nil {
		t.Fatal("remember succeeded after required auto-prune audit failed")
	}

	db, err = store.Open(store.StoreDir(dataDir, store.DefaultStoreName))
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer db.Close()
	active, err := db.GetAllActiveInsights()
	if err != nil {
		t.Fatalf("get active insights: %v", err)
	}
	if len(active) != 1 || active[0].ID != "rollback-old" {
		t.Fatalf("active insights after rollback = %v, want only rollback-old", active)
	}
}
