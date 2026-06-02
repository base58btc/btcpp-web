package getters

import (
	"mime"
	"path/filepath"
	"strings"

	"btcpp-web/external/spaces"
	"btcpp-web/internal/imgproc"
)

func uploadFilePostgres(contentType, filename string, data []byte) (string, error) {
	contentType = normalizeUploadContentType(contentType)
	key := postgresUploadKey(contentType, filename, data)
	return spaces.Upload(key, data, contentType, "")
}

func postgresUploadKey(contentType, filename string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		if exts, _ := mime.ExtensionsByType(contentType); len(exts) > 0 {
			ext = exts[0]
		}
	}
	return "uploads/" + imgproc.ShortID(data) + ext
}

func normalizeUploadContentType(contentType string) string {
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		return "application/octet-stream"
	}
	return contentType
}
