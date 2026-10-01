package scanner

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func relPaths(files []File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = filepath.ToSlash(f.RelPath)
	}
	sort.Strings(out)
	return out
}

func TestScan_OnlyMedia(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "DCIM/100MSDCF/DSC00001.JPG"))
	writeFile(t, filepath.Join(root, "DCIM/100MSDCF/C0001.MP4"))
	writeFile(t, filepath.Join(root, "readme.txt")) // 不是媒体也不是 sidecar，跳过

	files, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	got := relPaths(files)
	want := []string{
		"DCIM/100MSDCF/C0001.MP4",
		"DCIM/100MSDCF/DSC00001.JPG",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want[%d]=%q", i, got[i], i, want[i])
		}
	}
}

// 关键回归：SONY 卡上 PRIVATE/M4ROOT/THMBNL/*.xml 和 PRIVATE/DATABASE/
// 里的 .xml 是相机内部状态，没有同名媒体伴随，应该被过滤掉。
// 旧实现只看扩展名，这些 .xml 都会被收入，跟着一起拷到目标——污染。
func TestScan_OrphanSidecarsAreSkipped(t *testing.T) {
	root := t.TempDir()
	// 真正的视频
	writeFile(t, filepath.Join(root, "PRIVATE/M4ROOT/CLIP/C0001.MP4"))
	// 相机内部状态：孤立 .xml（无同名媒体）
	writeFile(t, filepath.Join(root, "PRIVATE/M4ROOT/THMBNL/CTRL_INF.xml"))
	writeFile(t, filepath.Join(root, "PRIVATE/DATABASE/MEDIAPRO.xml"))
	writeFile(t, filepath.Join(root, "PRIVATE/SONY/PRO/SETUP.xml"))

	files, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	got := relPaths(files)
	want := []string{"PRIVATE/M4ROOT/CLIP/C0001.MP4"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// 配对的 sidecar 应该跟随媒体被收入：C0001.MP4 + C0001.XML 同目录。
func TestScan_PairedSidecarKept(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "PRIVATE/M4ROOT/CLIP/C0001.MP4"))
	writeFile(t, filepath.Join(root, "PRIVATE/M4ROOT/CLIP/C0001.XML"))

	files, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	got := relPaths(files)
	want := []string{
		"PRIVATE/M4ROOT/CLIP/C0001.MP4",
		"PRIVATE/M4ROOT/CLIP/C0001.XML",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q, want[%d]=%q", i, got[i], i, want[i])
		}
	}
}

// 同 base 但不同目录的不算配对（避免误把 /A/foo.xml 当成 /B/foo.MP4 的伴生）。
func TestScan_SidecarMustBeSameDirectory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "videos/C0001.MP4"))
	writeFile(t, filepath.Join(root, "metadata/C0001.xml")) // 不同目录

	files, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	got := relPaths(files)
	want := []string{"videos/C0001.MP4"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// 大小写不敏感：C0001.MP4 配对 c0001.xml 也应保留（FAT/exFAT 卷标可能丢大小写）。
func TestScan_PairingIsCaseInsensitive(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "C0001.MP4"))
	writeFile(t, filepath.Join(root, "c0001.xml"))

	files, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d (%v)", len(files), relPaths(files))
	}
}

func TestIsRawVideoExt(t *testing.T) {
	for _, n := range []string{"a.CR3", "b.nef", "c.ARW", "d.DNG", "e.RAF", "f.RW2"} {
		if !IsRawExt(n) {
			t.Fatalf("%s should be raw", n)
		}
	}
	for _, n := range []string{"a.MP4", "b.mov", "c.MTS", "d.avi"} {
		if !IsVideoExt(n) {
			t.Fatalf("%s should be video", n)
		}
	}
	for _, n := range []string{"a.JPG", "b.HEIC", "c.XML"} {
		if IsRawExt(n) || IsVideoExt(n) {
			t.Fatalf("%s should be photo/other", n)
		}
	}
}
