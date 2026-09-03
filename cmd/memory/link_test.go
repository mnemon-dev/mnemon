package memory

import (
	"strings"
	"testing"

	"github.com/mnemon-dev/mnemon/internal/memory/store"
)

func TestLinkRejectsEmptyCLITypeBeforeServiceDefault(t *testing.T) {
	oldDataDir, oldStoreName, oldReadOnly := dataDir, storeName, readOnly
	oldType, oldWeight, oldMeta := linkType, linkWeight, linkMeta
	t.Cleanup(func() {
		dataDir, storeName, readOnly = oldDataDir, oldStoreName, oldReadOnly
		linkType, linkWeight, linkMeta = oldType, oldWeight, oldMeta
	})

	dataDir = t.TempDir()
	storeName = ""
	readOnly = false
	linkType = ""
	linkWeight = 0.5
	linkMeta = ""

	err := linkCmd.RunE(linkCmd, []string{"source", "target"})
	if err == nil || !strings.Contains(err.Error(), `invalid edge type ""`) {
		t.Fatalf("link error = %v, want empty edge type rejection", err)
	}
	if store.StoreExists(dataDir, store.DefaultStoreName) {
		t.Fatal("invalid CLI input opened a store before being rejected")
	}
}
