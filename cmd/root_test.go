package cmd

import (
	"strings"
	"testing"
)

func TestOpenDBRejectsInvalidStoreNameFromEnv(t *testing.T) {
	t.Setenv("MNEMON_STORE", "../outside")

	oldDataDir, oldStoreName, oldReadOnly := dataDir, storeName, readOnly
	t.Cleanup(func() {
		dataDir, storeName, readOnly = oldDataDir, oldStoreName, oldReadOnly
	})
	dataDir = t.TempDir()
	storeName = ""
	readOnly = false

	db, err := openDB()
	if err == nil {
		if db != nil {
			db.Close()
		}
		t.Fatal("expected invalid store name error")
	}
	if !strings.Contains(err.Error(), "invalid store name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenDBRejectsInvalidStoreNameFromFlag(t *testing.T) {
	oldDataDir, oldStoreName, oldReadOnly := dataDir, storeName, readOnly
	t.Cleanup(func() {
		dataDir, storeName, readOnly = oldDataDir, oldStoreName, oldReadOnly
	})
	dataDir = t.TempDir()
	storeName = "../outside"
	readOnly = false

	db, err := openDB()
	if err == nil {
		if db != nil {
			db.Close()
		}
		t.Fatal("expected invalid store name error")
	}
	if !strings.Contains(err.Error(), "invalid store name") {
		t.Fatalf("unexpected error: %v", err)
	}
}
