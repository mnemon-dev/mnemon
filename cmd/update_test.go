package cmd

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestNativeUpdateCommandRequiresNPMLauncher(t *testing.T) {
	t.Parallel()
	command := updateCommand()
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs(nil)
	err := command.ExecuteContext(context.Background())
	if err == nil ||
		!strings.Contains(err.Error(), "npm install --global @mnemon-dev/mnemon@latest") {
		t.Fatalf("error = %v", err)
	}
}
