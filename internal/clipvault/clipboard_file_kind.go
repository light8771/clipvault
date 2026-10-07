package clipvault

import "strings"

// classifyFileDropKind chooses the most representative content kind for a
// physical file-drop payload. It is independent from the source application.
func classifyFileDropKind(paths []string) string {
	counts := make(map[string]int, len(paths))
	for _, path := range paths {
		counts[fileExtKind(path)]++
	}
	if len(counts) == 1 {
		for kind := range counts {
			return kind
		}
	}
	best, max := "files", 0
	tied := false
	for kind, count := range counts {
		if count > max {
			best, max = kind, count
			tied = false
		} else if count == max {
			tied = true
		}
	}
	if tied {
		return "files"
	}
	return best
}

var fileKindExtensions = map[string][]string{
	"image":   {"jpg", "jpeg", "png", "gif", "bmp", "webp", "svg", "ico", "tiff", "tif", "heic", "heif", "raw", "cr2", "nef", "psd", "ai", "avif"},
	"video":   {"mp4", "avi", "mov", "mkv", "wmv", "flv", "webm", "m4v", "mpeg", "mpg", "3gp", "ts", "rmvb", "rm", "asf", "f4v", "vob"},
	"archive": {"zip", "rar", "7z", "tar", "gz", "bz2", "xz", "iso", "cab", "lzh", "z", "tgz", "tbz2", "ace", "arj", "zst", "lz4"},
	"office":  {"doc", "docx", "xls", "xlsx", "ppt", "pptx", "odt", "ods", "odp", "pdf", "wps", "et", "dps", "csv", "rtf", "pages", "numbers", "key"},
}

func fileExtension(path string) string {
	name := strings.ReplaceAll(path, `\`, "/")
	name = name[strings.LastIndex(name, "/")+1:]
	dot := strings.LastIndex(name, ".")
	if dot < 0 {
		return ""
	}
	return strings.ToLower(name[dot+1:])
}

func fileExtKind(path string) string {
	ext := fileExtension(path)
	for kind, extensions := range fileKindExtensions {
		for _, candidate := range extensions {
			if ext == candidate {
				return kind
			}
		}
	}
	return "files"
}
