package static

import (
	"path/filepath"
	"strings"
)

var defaultMIMETypes = map[string]string{
	// Text
	".html": "text/html; charset=utf-8",
	".htm":  "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "application/javascript; charset=utf-8",
	".mjs":  "application/javascript; charset=utf-8",
	".json": "application/json; charset=utf-8",
	".xml":  "application/xml; charset=utf-8",
	".txt":  "text/plain; charset=utf-8",
	".csv":  "text/csv; charset=utf-8",
	".md":   "text/markdown; charset=utf-8",
	".yaml": "text/yaml; charset=utf-8",
	".yml":  "text/yaml; charset=utf-8",
	".toml": "application/toml; charset=utf-8",
	".ics":  "text/calendar; charset=utf-8",

	// Images
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
	".svg":  "image/svg+xml; charset=utf-8",
	".ico":  "image/x-icon",
	".bmp":  "image/bmp",
	".tiff": "image/tiff",
	".tif":  "image/tiff",
	".heic": "image/heic",
	".heif": "image/heif",
	".jxl":  "image/jxl",

	// Fonts
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".eot":   "application/vnd.ms-fontobject",

	// Audio
	".mp3":  "audio/mpeg",
	".ogg":  "audio/ogg",
	".wav":  "audio/wav",
	".flac": "audio/flac",
	".aac":  "audio/aac",
	".m4a":  "audio/mp4",
	".opus": "audio/opus",

	// Video
	".mp4":  "video/mp4",
	".webm": "video/webm",
	".ogv":  "video/ogg",
	".avi":  "video/x-msvideo",
	".mov":  "video/quicktime",
	".mkv":  "video/x-matroska",
	".m4v":  "video/mp4",

	// Documents
	".pdf":  "application/pdf",
	".doc":  "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xls":  "application/vnd.ms-excel",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".ppt":  "application/vnd.ms-powerpoint",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".odt":  "application/vnd.oasis.opendocument.text",
	".ods":  "application/vnd.oasis.opendocument.spreadsheet",

	// Archives
	".zip": "application/zip",
	".gz":  "application/gzip",
	".tar": "application/x-tar",
	".bz2": "application/x-bzip2",
	".7z":  "application/x-7z-compressed",
	".rar": "application/vnd.rar",
	".zst": "application/zstd",
	".xz":  "application/x-xz",

	// WebAssembly & Maps
	".wasm": "application/wasm",
	".map":  "application/json",

	// Manifest & Config
	".webmanifest": "application/manifest+json",
	".appcache":    "text/cache-manifest",

	// Misc
	".atom": "application/atom+xml",
	".rss":  "application/rss+xml",
	".wsdl": "application/wsdl+xml",
	".xsl":  "application/xslt+xml",
	".xslt": "application/xslt+xml",
	".swf":  "application/x-shockwave-flash",
}

// MIMERegistry resolves file extensions to content types.
type MIMERegistry struct {
	types map[string]string
}

func NewMIMERegistry(custom map[string]string) *MIMERegistry {
	m := &MIMERegistry{
		types: make(map[string]string, len(defaultMIMETypes)+len(custom)),
	}
	for k, v := range defaultMIMETypes {
		m.types[k] = v
	}
	for k, v := range custom {
		if !strings.HasPrefix(k, ".") {
			k = "." + k
		}
		m.types[strings.ToLower(k)] = v
	}
	return m
}

// Lookup returns the MIME type for a file path.
func (m *MIMERegistry) Lookup(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if ct, ok := m.types[ext]; ok {
		return ct
	}
	return "application/octet-stream"
}
