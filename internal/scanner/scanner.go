package scanner

import (
	"io/fs"
	"path/filepath"
	"strings"
)

var MediaExtensions = map[string]bool{
	".mp4": true, ".mov": true, ".m4v": true,
	".avi": true, ".mkv": true, ".mts": true, ".m2ts": true,
	".jpg": true, ".jpeg": true,
	".arw": true, ".raw": true, ".cr2": true, ".cr3": true,
	".nef": true, ".dng": true, ".heic": true, ".png": true,
	".wav": true, ".mp3": true, ".aac": true, ".bwf": true,
}

var SidecarExtensions = map[string]bool{
	".thm": true, ".xml": true, ".srt": true, ".lrc": true,
}

type File struct {
	Path    string
	RelPath string
	Size    int64
	Info    fs.FileInfo
	IsMedia bool
}

func IsMedia(name string) bool {
	return MediaExtensions[strings.ToLower(filepath.Ext(name))]
}

// RawExtensions 是需要进 raw/ 子目录的格式。
var RawExtensions = map[string]bool{
	".arw": true, ".raw": true, ".cr2": true, ".cr3": true,
	".nef": true, ".dng": true, ".raf": true, ".rw2": true,
	".orf": true, ".pef": true, ".x3f": true, ".srw": true,
}

// IsRawExt 判断是否为 RAW 格式（进 raw/ 子目录）。
func IsRawExt(name string) bool {
	return RawExtensions[strings.ToLower(filepath.Ext(name))]
}

// VideoExtensions 是进 video/ 子目录的视频格式。
var VideoExtensions = map[string]bool{
	".mp4": true, ".mov": true, ".m4v": true,
	".avi": true, ".mkv": true, ".mts": true, ".m2ts": true,
}

// IsVideoExt 判断是否为视频格式（进 video/ 子目录）。
func IsVideoExt(name string) bool {
	return VideoExtensions[strings.ToLower(filepath.Ext(name))]
}

func IsSidecar(name string) bool {
	return SidecarExtensions[strings.ToLower(filepath.Ext(name))]
}

// Scan 遍历 root 收集媒体文件。Sidecar（.xml/.thm/.srt/.lrc）只在
// **同目录存在同 base name 的媒体文件**时才一并收入——这样能避开相机
// 内部的状态文件（SONY 的 PRIVATE/M4ROOT/THMBNL/*.xml、PRIVATE/DATABASE
// 等），那些 .xml 与用户素材没有 base name 对应关系，是数据库 / 索引文件，
// 拷过去只会污染目标目录。
//
// 算法：先一次遍历找出所有"媒体 base name"（按目录 + 文件名 stem 索引），
// 然后第二次遍历时把孤立 sidecar 过滤掉。
func Scan(root string) ([]File, error) {
	var allEntries []File
	mediaStems := map[string]bool{} // key: dir + "\x00" + stem(lowercased)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		isMedia := IsMedia(name)
		if !isMedia && !IsSidecar(name) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		f := File{
			Path:    path,
			RelPath: rel,
			Size:    info.Size(),
			Info:    info,
			IsMedia: isMedia,
		}
		allEntries = append(allEntries, f)
		if isMedia {
			mediaStems[stemKey(path)] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	out := make([]File, 0, len(allEntries))
	for _, f := range allEntries {
		if f.IsMedia {
			out = append(out, f)
			continue
		}
		// sidecar：仅在同目录有同 base 媒体文件时保留
		if mediaStems[stemKey(f.Path)] {
			out = append(out, f)
		}
	}
	return out, nil
}

// stemKey 把 "/dir/foo.JPG" 映射成 "/dir\x00foo"（小写 stem），
// 用作 mediaStems 的 key。\x00 不会出现在文件名里，安全做分隔符。
func stemKey(path string) string {
	dir, base := filepath.Split(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	return dir + "\x00" + strings.ToLower(stem)
}
