package memory

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestCaptureStdoutRetainsOutputLargerThanPipeBuffer(t *testing.T) {
	want := strings.Repeat("memory evidence\n", 1<<16)
	previous := os.Stdout
	got := captureStdout(t, func() {
		if _, err := io.WriteString(os.Stdout, want); err != nil {
			t.Fatal(err)
		}
	})
	if got != want {
		t.Fatalf("stdout capture lost content: got %d bytes, want %d", len(got), len(want))
	}
	if os.Stdout != previous {
		t.Fatal("stdout was not restored")
	}
}
