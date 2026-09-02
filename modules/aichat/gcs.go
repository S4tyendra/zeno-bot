package aichat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

const (
	gcsBucket   = "aidatax"
	gcsMaxBytes = 80 << 20
)

var gcsHTTP = &http.Client{Timeout: 60 * time.Second}

func gcsObjectName(chatID int64, msgID int32, fileName string) string {
	base := filepath.Base(fileName)
	base = strings.TrimSpace(base)
	base = strings.ReplaceAll(base, "/", "_")
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "file"
	}
	return fmt.Sprintf("%d/%d/%s", chatID, msgID, base)
}

func gcsURI(object string) string {
	return "gs://" + gcsBucket + "/" + object
}

func gcsObjectFromURI(uri string) string {
	return strings.TrimPrefix(uri, "gs://"+gcsBucket+"/")
}

func gcsAuth(ctx context.Context) (string, error) {
	if vertexCreds == nil {
		return "", fmt.Errorf("vertex credentials not initialized")
	}
	tok, err := vertexCreds.Token(ctx)
	if err != nil {
		return "", err
	}
	typ := tok.Type
	if typ == "" {
		typ = "Bearer"
	}
	return typ + " " + tok.Value, nil
}

func gcsUpload(ctx context.Context, object, mime string, data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("empty file")
	}
	if int64(len(data)) > gcsMaxBytes {
		return "", fmt.Errorf("file too large (%d bytes)", len(data))
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	auth, err := gcsAuth(ctx)
	if err != nil {
		return "", err
	}
	u := fmt.Sprintf("https://storage.googleapis.com/upload/storage/v1/b/%s/o?uploadType=media&name=%s",
		gcsBucket, url.QueryEscape(object))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Type", mime)
	resp, err := gcsHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Overwrite needs storage.objects.delete; if the object is already there, reuse it.
		if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusConflict) && gcsObjectExists(ctx, gcsURI(object)) {
			log.Printf("[AiChat] GCS object already exists, reusing gs://%s/%s", gcsBucket, object)
			return gcsURI(object), nil
		}
		return "", fmt.Errorf("gcs upload %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	uri := gcsURI(object)
	log.Printf("[AiChat] GCS uploaded %s (%d bytes, %s)", uri, len(data), mime)
	return uri, nil
}

func gcsObjectExists(ctx context.Context, uri string) bool {
	if uri == "" || !strings.HasPrefix(uri, "gs://"+gcsBucket+"/") {
		return false
	}
	object := gcsObjectFromURI(uri)
	if object == "" || object == uri {
		return false
	}
	auth, err := gcsAuth(ctx)
	if err != nil {
		log.Printf("[AiChat] GCS auth for exists check failed: %v", err)
		return false
	}
	u := fmt.Sprintf("https://storage.googleapis.com/storage/v1/b/%s/o/%s", gcsBucket, url.PathEscape(object))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", auth)
	resp, err := gcsHTTP.Do(req)
	if err != nil {
		log.Printf("[AiChat] GCS exists check failed: %v", err)
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
	return resp.StatusCode == http.StatusOK
}

func fileProbablyLive(uploadedAt time.Time) bool {
	return time.Since(uploadedAt) < 20*time.Hour
}

func fileDefinitelyExpired(uploadedAt time.Time) bool {
	return time.Since(uploadedAt) > 24*time.Hour
}
