package downloader

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\x00", "")
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "%", "_")
	switch name {
	case "", ".", "..":
		return ""
	}
	return name
}

func preserveExt(name, original string) string {
	if name == "" {
		return ""
	}
	if filepath.Ext(name) != "" {
		return name
	}
	if ext := filepath.Ext(original); ext != "" {
		return name + ext
	}
	return name
}

func downloadsRoot() string {
	return filepath.Join("data", "downloads")
}

func taskDir(taskID int64) string {
	return filepath.Join(downloadsRoot(), fmt.Sprintf("%d", taskID))
}

func isUnderDownloads(path string) bool {
	if path == "" {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	root, err := filepath.Abs(downloadsRoot())
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isM3U8URL(rawURL string) bool {
	u := strings.ToLower(strings.TrimSpace(rawURL))
	if strings.Contains(u, ".m3u8") {
		return true
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.HasSuffix(strings.ToLower(parsed.Path), ".m3u8")
}

func isMediaSiteURL(rawURL string) bool {
	host := hostnameFromURL(rawURL)
	if host == "" {
		return false
	}
	domains := []string{
		"youtube.com", "youtu.be", "instagram.com", "instagr.am",
		"twitter.com", "x.com", "tiktok.com", "reddit.com",
		"vimeo.com", "facebook.com", "fb.watch", "twitch.tv",
		"soundcloud.com", "bilibili.com", "dailymotion.com",
	}
	for _, d := range domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

func hostnameFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		parsed, err = url.Parse("https://" + rawURL)
		if err != nil {
			return ""
		}
	}
	return strings.ToLower(parsed.Hostname())
}

func parseUploadMode(s string) UploadMode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "doc", "document", "file":
		return UploadDocument
	case "video", "media":
		return UploadVideo
	case "audio", "music", "mp3":
		return UploadAudio
	case "photo", "image", "pic", "picture":
		return UploadPhoto
	default:
		return UploadAuto
	}
}

func filenameFromDisposition(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	lower := strings.ToLower(v)
	if i := strings.Index(lower, "filename*="); i >= 0 {
		rest := v[i+len("filename*="):]
		if comma := strings.Index(rest, ";"); comma >= 0 {
			rest = rest[:comma]
		}
		rest = strings.Trim(rest, `"`)
		if j := strings.LastIndex(rest, "''"); j >= 0 {
			rest = rest[j+2:]
		}
		if decoded, err := url.QueryUnescape(rest); err == nil {
			rest = decoded
		}
		return sanitizeFileName(rest)
	}
	if i := strings.Index(lower, "filename="); i >= 0 {
		rest := v[i+len("filename="):]
		if comma := strings.Index(rest, ";"); comma >= 0 {
			rest = rest[:comma]
		}
		return sanitizeFileName(strings.Trim(rest, `" `))
	}
	return ""
}
