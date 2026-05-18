package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEjectMemoryBlockRemovesMarkedBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	input := "before\n\n<!-- mnemon:start -->\nremove me\n<!-- mnemon:end -->\n\nafter\n"
	if err := os.WriteFile(path, []byte(input), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	changed, err := EjectMemoryBlock(path)
	if err != nil {
		t.Fatalf("eject block: %v", err)
	}
	if !changed {
		t.Fatal("expected file to change")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if string(got) != "before\n\nafter\n" {
		t.Fatalf("unexpected content:\n%s", got)
	}
}

func TestEjectMemoryBlockIgnoresEndMarkerBeforeStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	input := "legacy <!-- mnemon:end --> marker\n<!-- mnemon:start -->\nremove\n<!-- mnemon:end -->\nkeep\n"
	if err := os.WriteFile(path, []byte(input), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	changed, err := EjectMemoryBlock(path)
	if err != nil {
		t.Fatalf("eject block: %v", err)
	}
	if !changed {
		t.Fatal("expected file to change")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	want := "legacy <!-- mnemon:end --> marker\nkeep\n"
	if string(got) != want {
		t.Fatalf("content mismatch:\nwant %q\ngot  %q", want, string(got))
	}
}

func TestEjectMemoryBlockRemovesFileWhenOnlyBlockRemains(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	if err := os.WriteFile(path, []byte("<!-- mnemon:start -->\nremove\n<!-- mnemon:end -->\n"), 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	changed, err := EjectMemoryBlock(path)
	if err != nil {
		t.Fatalf("eject block: %v", err)
	}
	if !changed {
		t.Fatal("expected file to change")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file removal, got err=%v", err)
	}
}
