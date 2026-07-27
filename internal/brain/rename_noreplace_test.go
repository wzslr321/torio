package brain

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRenameNoReplaceRefusesAnExistingEmptyDestination(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	destination := filepath.Join(parent, "destination")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatalf("Mkdir source: %v", err)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatalf("Mkdir destination: %v", err)
	}
	writeHostTransferFile(t, source, "source.md", "source", 0o600)
	writeHostTransferFile(t, destination, "destination.md", "destination", 0o600)

	if err := renameNoReplace(source, destination); err == nil {
		t.Fatal("renameNoReplace replaced an existing destination")
	}
	for _, target := range []string{
		filepath.Join(source, "source.md"),
		filepath.Join(destination, "destination.md"),
	} {
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("preserved path missing: %v", err)
		}
	}
}
