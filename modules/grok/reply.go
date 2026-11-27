package grok

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/amarnathcjd/gogram/telegram"

	"zeno/connectors"
	"zeno/db"
)

const (
	maxUpload    = 1 << 30 // 1 GiB per callback
	maxText      = 4000
	maxCaption   = 1000
	maxFileFetch = 512 << 20
)

// jsonFile is a file in a JSON callback: either a URL to fetch or base64 data.
type jsonFile struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Data string `json:"data"`
}

// jsonReply is the JSON callback body. "reply" may also be sent as "text" or "message".
type jsonReply struct {
	ID      string          `json:"id"`
	Reply   string          `json:"reply"`
	Text    string          `json:"text"`
	Message string          `json:"message"`
	Files   json.RawMessage `json:"files"`
}

type target struct {
	chatID  int64
	msgID   int32
	replied bool
}

type localFile struct {
	path string
	name string
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"ok": false, "error": msg})
}

func handleReply(w http.ResponseWriter, r *http.Request) {
	// Only callbacks carrying "meta-key: Satya" are processed; everything else gets a silent ok.
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("meta-key")), "satya") {
		io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":    true,
			"usage": "POST id + reply (+ files) as multipart/form-data, or JSON {\"id\":\"...\",\"reply\":\"...\",\"files\":[{\"name\":\"a.png\",\"url\":\"https://...\"}]}",
		})
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		fail(w, http.StatusMethodNotAllowed, "use POST")
		return
	}
	if botClient == nil {
		fail(w, http.StatusServiceUnavailable, "bot not ready")
		return
	}

	tmp, err := os.MkdirTemp("", "grok-out-*")
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.RemoveAll(tmp)

	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))

	var id, text string
	var files []localFile

	switch {
	case ct == "multipart/form-data":
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			fail(w, http.StatusBadRequest, "bad multipart: "+err.Error())
			return
		}
		defer r.MultipartForm.RemoveAll()
		id = firstForm(r, "id")
		text = firstForm(r, "reply", "text", "message")
		for _, fhs := range r.MultipartForm.File {
			for _, fh := range fhs {
				lf, err := saveUpload(tmp, len(files), fh.Filename, func() (io.ReadCloser, error) { return fh.Open() })
				if err != nil {
					fail(w, http.StatusBadRequest, "file "+fh.Filename+": "+err.Error())
					return
				}
				files = append(files, lf)
			}
		}
	case ct == "application/x-www-form-urlencoded":
		if err := r.ParseForm(); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		id = firstForm(r, "id")
		text = firstForm(r, "reply", "text", "message")
	default: // JSON (also when Content-Type is missing)
		var body jsonReply
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			fail(w, http.StatusBadRequest, "bad json: "+err.Error())
			return
		}
		id = strings.TrimSpace(body.ID)
		text = firstNonEmpty(body.Reply, body.Text, body.Message)
		jf, err := decodeJSONFiles(body.Files)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		for _, f := range jf {
			lf, err := fetchJSONFile(r.Context(), tmp, len(files), f)
			if err != nil {
				fail(w, http.StatusBadRequest, "file "+f.Name+": "+err.Error())
				return
			}
			files = append(files, lf)
		}
	}

	if id == "" {
		fail(w, http.StatusBadRequest, "missing id")
		return
	}
	if strings.TrimSpace(text) == "" && len(files) == 0 {
		fail(w, http.StatusBadRequest, "nothing to send: give reply and/or files")
		return
	}
	t, err := lookup(r.Context(), id)
	if err != nil {
		fail(w, http.StatusNotFound, err.Error())
		return
	}

	sent, err := deliver(t, text, files)
	if err != nil {
		log.Printf("[Grok] deliver %s: %v", id, err)
		fail(w, http.StatusBadGateway, "telegram: "+err.Error())
		return
	}
	if db.Pool != nil {
		db.Pool.Exec(context.Background(), `UPDATE grok_requests SET replied = TRUE WHERE id = $1`, id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id, "messages": sent, "files": len(files)})
}

func firstForm(r *http.Request, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(r.FormValue(k)); v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// lookup resolves an id we issued. Unknown ids are rejected so the endpoint can't post to arbitrary chats.
func lookup(ctx context.Context, id string) (target, error) {
	var t target
	if db.Pool == nil {
		return t, errors.New("db unavailable")
	}
	err := db.Pool.QueryRow(ctx, `SELECT chat_id, msg_id, replied FROM grok_requests WHERE id = $1`, id).Scan(&t.chatID, &t.msgID, &t.replied)
	if err != nil {
		return t, fmt.Errorf("unknown id %q", id)
	}
	return t, nil
}

func decodeJSONFiles(raw json.RawMessage) ([]jsonFile, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var objs []jsonFile
	if err := json.Unmarshal(raw, &objs); err == nil {
		return objs, nil
	}
	var urls []string
	if err := json.Unmarshal(raw, &urls); err == nil {
		for _, u := range urls {
			objs = append(objs, jsonFile{URL: u})
		}
		return objs, nil
	}
	return nil, errors.New("files must be [{name,url|data}] or [\"url\"]")
}

func saveUpload(dir string, idx int, name string, open func() (io.ReadCloser, error)) (localFile, error) {
	name = cleanName(name, idx)
	rc, err := open()
	if err != nil {
		return localFile{}, err
	}
	defer rc.Close()
	sub := filepath.Join(dir, strconv.Itoa(idx))
	if err := os.MkdirAll(sub, 0o755); err != nil {
		return localFile{}, err
	}
	p := filepath.Join(sub, name)
	f, err := os.Create(p)
	if err != nil {
		return localFile{}, err
	}
	defer f.Close()
	if _, err := io.Copy(f, rc); err != nil {
		return localFile{}, err
	}
	return localFile{path: p, name: name}, nil
}

func fetchJSONFile(ctx context.Context, dir string, idx int, jf jsonFile) (localFile, error) {
	if jf.Data != "" {
		data := jf.Data
		if i := strings.Index(data, ";base64,"); i >= 0 && strings.HasPrefix(data, "data:") {
			data = data[i+8:]
		}
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return localFile{}, fmt.Errorf("bad base64: %w", err)
		}
		return saveUpload(dir, idx, jf.Name, func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(string(raw))), nil
		})
	}
	if jf.URL == "" {
		return localFile{}, errors.New("needs url or data")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jf.URL, nil)
	if err != nil {
		return localFile{}, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return localFile{}, err
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return localFile{}, fmt.Errorf("GET %s: %s", jf.URL, resp.Status)
	}
	name := jf.Name
	if name == "" {
		if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil {
			name = params["filename"]
		}
	}
	if name == "" {
		name = filepath.Base(strings.SplitN(req.URL.Path, "?", 2)[0])
	}
	return saveUpload(dir, idx, name, func() (io.ReadCloser, error) {
		return struct {
			io.Reader
			io.Closer
		}{io.LimitReader(resp.Body, maxFileFetch), resp.Body}, nil
	})
}

func cleanName(name string, idx int) string {
	n := connectors.SafeName(filepath.Base(strings.ReplaceAll(name, "\\", "/")))
	if n == "" {
		n = fmt.Sprintf("file_%d", idx+1)
	}
	return n
}

func isVisual(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".mp4", ".mov", ".webm":
		return true
	}
	return false
}

func isPhoto(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	}
	return false
}

// chunks splits text into Telegram-sized pieces on line boundaries where possible.
func chunks(s string, n int) []string {
	r := []rune(strings.TrimSpace(s))
	var out []string
	for len(r) > n {
		cut := n
		for i := n - 1; i > n/2; i-- {
			if r[i] == '\n' {
				cut = i
				break
			}
		}
		out = append(out, strings.TrimSpace(string(r[:cut])))
		r = []rune(strings.TrimSpace(string(r[cut:])))
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}

// editOrSend edits the status message the first time, and replies to it after that.
func editOrSend(t *target, text string, opts *telegram.SendOptions) (int, error) {
	if !t.replied {
		if _, err := botClient.EditMessage(t.chatID, t.msgID, text, opts); err == nil {
			t.replied = true
			return 1, nil
		} else if opts.ParseMode != "" {
			plain := *opts
			plain.ParseMode = ""
			if _, err2 := botClient.EditMessage(t.chatID, t.msgID, text, &plain); err2 == nil {
				t.replied = true
				return 1, nil
			}
			return 0, err
		} else {
			return 0, err
		}
	}
	o := *opts
	o.ReplyID = t.msgID
	if _, err := botClient.SendMessage(t.chatID, text, &o); err != nil {
		if o.ParseMode == "" {
			return 0, err
		}
		o.ParseMode = ""
		if _, err := botClient.SendMessage(t.chatID, text, &o); err != nil {
			return 0, err
		}
	}
	return 1, nil
}

func deliver(t target, text string, files []localFile) (int, error) {
	sent := 0
	md := &telegram.SendOptions{ParseMode: "Markdown"}

	// One file and a short reply: put the file on the status message itself.
	if len(files) == 1 && utf8.RuneCountInString(text) <= maxCaption && !t.replied {
		f := files[0]
		opts := &telegram.SendOptions{ParseMode: "Markdown", Media: f.path, FileName: f.name, ForceDocument: !isVisual(f.name)}
		if _, err := botClient.EditMessage(t.chatID, t.msgID, text, opts); err == nil {
			return 1, nil
		} else {
			log.Printf("[Grok] edit with media failed, falling back: %v", err)
		}
	}

	for _, c := range chunks(text, maxText) {
		n, err := editOrSend(&t, c, md)
		if err != nil {
			return sent, err
		}
		sent += n
	}
	if len(files) == 0 {
		return sent, nil
	}
	if !t.replied && strings.TrimSpace(text) == "" {
		botClient.EditMessage(t.chatID, t.msgID, "📎 Grok sent files:")
	}

	n, err := sendFiles(t, files)
	return sent + n, err
}

func sendFiles(t target, files []localFile) (int, error) {
	if len(files) == 1 {
		f := files[0]
		_, err := botClient.SendMedia(t.chatID, f.path, &telegram.MediaOptions{ReplyID: t.msgID, FileName: f.name, ForceDocument: !isVisual(f.name)})
		if err != nil {
			return 0, err
		}
		return 1, nil
	}

	allPhotos := true
	for _, f := range files {
		if !isPhoto(f.name) {
			allPhotos = false
		}
	}
	sent := 0
	for i := 0; i < len(files); i += 10 {
		end := min(i+10, len(files))
		paths := make([]string, 0, end-i)
		for _, f := range files[i:end] {
			paths = append(paths, f.path)
		}
		if len(paths) > 1 {
			msgs, err := botClient.SendAlbum(t.chatID, paths, &telegram.MediaOptions{ReplyID: t.msgID, ForceDocument: !allPhotos})
			if err == nil {
				sent += len(msgs)
				continue
			}
			log.Printf("[Grok] album failed, sending one by one: %v", err)
		}
		for _, f := range files[i:end] {
			if _, err := botClient.SendMedia(t.chatID, f.path, &telegram.MediaOptions{ReplyID: t.msgID, FileName: f.name, ForceDocument: !isVisual(f.name)}); err != nil {
				return sent, err
			}
			sent++
		}
	}
	return sent, nil
}
