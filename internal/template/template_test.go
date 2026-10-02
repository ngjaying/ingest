package template

import "testing"

func TestDatePath(t *testing.T) {
	ctx := Context{DatePath: "2026/202609/20260910", DatePathEnd: ""}
	got, err := Render("{date_path}[_{date_path_end}]", ctx)
	if err != nil || got != "2026/202609/20260910" {
		t.Fatalf("single day: %q %v", got, err)
	}
	if got, err := Render("{date_path}", ctx); err != nil || got != "2026/202609/20260910" {
		t.Fatalf("path: %q %v", got, err)
	}
}
