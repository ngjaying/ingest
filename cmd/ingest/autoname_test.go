package main

import (
	"testing"
	"time"

	"github.com/hktkzyx/ingest/internal/period"
)

func mkSeg(start, end string) period.Segment {
	s, _ := time.Parse("2006-01-02", start)
	e, _ := time.Parse("2006-01-02", end)
	return period.Segment{Start: s, End: e}
}

func TestAutoSegmentName(t *testing.T) {
	flagName = ""
	if got := autoSegmentName(mkSeg("2026-09-18", "2026-09-18"), false); got != "20260918" {
		t.Fatalf("single date: got %q", got)
	}
	if got := autoSegmentName(mkSeg("2026-09-18", "2026-09-20"), true); got != "20260918_20260920" {
		t.Fatalf("range: got %q", got)
	}
	flagName = "smoke"
	if got := autoSegmentName(mkSeg("2026-09-18", "2026-09-18"), false); got != "smoke" {
		t.Fatalf("single with name: got %q", got)
	}
	if got := autoSegmentName(mkSeg("2026-09-18", "2026-09-18"), true); got != "smoke-20260918" {
		t.Fatalf("multi with name prefix: got %q", got)
	}
	flagName = ""
}
