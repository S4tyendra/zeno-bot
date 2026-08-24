package artifacts

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"zeno/db"
)

var (
	ArtifactPort    string
	ArtifactBaseURL string
	ServerIP        string
)

func init() {
	ArtifactPort = os.Getenv("ARTIFACT_PORT")
	if ArtifactPort == "" {
		ArtifactPort = "8080"
	}
	ArtifactBaseURL = os.Getenv("ARTIFACT_BASE_URL")
	ServerIP = getOutboundIP()
}

func getOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().(*net.UDPAddr)
	return localAddr.IP.String()
}

// GetArtifactURL returns the clean URL for a given artifact ID.
func GetArtifactURL(artifactID string) string {
	if ArtifactBaseURL != "" {
		return fmt.Sprintf("%s/%s", strings.TrimRight(ArtifactBaseURL, "/"), artifactID)
	}
	return fmt.Sprintf("http://%s:%s/%s", ServerIP, ArtifactPort, artifactID)
}

func StartServer() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		artifactID := r.URL.Query().Get("artifact")
		if artifactID == "" {
			artifactID = strings.Trim(r.URL.Path, "/")
		}

		if artifactID == "" || artifactID == "favicon.ico" {
			w.WriteHeader(http.StatusOK)
			return
		}

		var filePath, mimeType string
		err := db.Pool.QueryRow(context.Background(), `SELECT file_path, mime_type FROM artifacts WHERE artifact_id = $1`, artifactID).Scan(&filePath, &mimeType)
		if err != nil {
			http.Error(w, "Artifact not found", http.StatusNotFound)
			return
		}

		f, err := os.Open(filepath.Clean(filePath))
		if err != nil {
			http.Error(w, "File not accessible", http.StatusNotFound)
			return
		}
		defer f.Close()

		w.Header().Set("Content-Type", mimeType)
		io.Copy(w, f)
	})

	addr := fmt.Sprintf("0.0.0.0:%s", ArtifactPort)
	log.Printf("[Artifacts] Starting HTTP server on %s", addr)
	go func() {
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Printf("[Artifacts] HTTP server failed: %v", err)
		}
	}()
}
