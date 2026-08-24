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

	"zeno/db"
)

var (
	ArtifactPort string
	ServerIP     string
)

func init() {
	ArtifactPort = os.Getenv("ARTIFACT_PORT")
	if ArtifactPort == "" {
		ArtifactPort = "8080"
	}
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

func StartServer() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		artifactID := r.URL.Query().Get("artifact")
		if artifactID == "" {
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
