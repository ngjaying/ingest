package main

import (
	"testing"

	"github.com/hktkzyx/ingest/internal/mount"
)

func TestClassifyVolumes(t *testing.T) {
	eDrive := "E:" + "\\"
	fDrive := "F:" + "\\"
	known := map[string]bool{eDrive: true}
	cur := []mount.Volume{{Path: eDrive}, {Path: fDrive}}
	added, gone := classifyVolumes(known, cur)
	if len(added) != 1 || added[0].Path != fDrive {
		t.Fatalf("added: %+v", added)
	}
	if len(gone) != 0 {
		t.Fatalf("gone: %+v", gone)
	}
	added, gone = classifyVolumes(map[string]bool{eDrive: true, fDrive: true}, cur[:1])
	if len(added) != 0 || len(gone) != 1 || gone[0] != fDrive {
		t.Fatalf("gone case: added=%+v gone=%+v", added, gone)
	}
}

func TestSanitizeLabel(t *testing.T) {
	if got := sanitizeLabel("E:" + "\\"); got != "E" {
		t.Fatalf("got %q", got)
	}
}
