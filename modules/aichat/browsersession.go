package aichat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"zeno/connectors"
)

// A browser session is one Obscura process for a private user, or one shared
// process for a group. Tool calls in a turn reuse it. The process is stopped
// when the last turn that might use it finishes, and tabs are written to disk
// so the next turn's system prompt and the next tool call can resume the day.

type savedTab struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

type savedBrowser struct {
	Current string     `json:"current"`
	Tabs    []savedTab `json:"tabs"`
}

type liveTab struct {
	id      string
	session string
	url     string
	title   string
}

type liveBrowser struct {
	profile string
	dayDir  string
	day     string
	cmd     *exec.Cmd
	port    int
	token   string
	ws      *websocket.Conn
	mu      sync.Mutex
	startMu sync.Mutex
	nextID  int
	pending map[int]chan cdpMsg
	tabs    []*liveTab
	current int
	refs    int
}

type cdpMsg struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

var (
	browsersMu sync.Mutex
	browsers   = map[string]*liveBrowser{}
)

func browserKey(private bool, chatID, userID int64) (key, profile, dayDir, day string) {
	day = time.Now().In(ist).Format("2006-01-02")
	profile = connectors.ScopeProfile(private, chatID, userID)
	dayDir = filepath.Join(profile, day)
	if private {
		key = fmt.Sprintf("user-%d/%s", userID, day)
	} else {
		key = fmt.Sprintf("chat-%d/%s", chatID, day)
	}
	return key, profile, dayDir, day
}

func beginBrowserTurn(private bool, chatID, userID int64) {
	key, _, _, _ := browserKey(private, chatID, userID)
	browsersMu.Lock()
	defer browsersMu.Unlock()
	b := browsers[key]
	if b == nil {
		b = &liveBrowser{}
		browsers[key] = b
	}
	b.refs++
}

func endBrowserTurn(private bool, chatID, userID int64) {
	key, profile, _, _ := browserKey(private, chatID, userID)
	browsersMu.Lock()
	b := browsers[key]
	if b == nil {
		browsersMu.Unlock()
		return
	}
	b.refs--
	if b.refs > 0 {
		browsersMu.Unlock()
		return
	}
	delete(browsers, key)
	browsersMu.Unlock()
	b.shutdown(profile)
}

func (b *liveBrowser) shutdown(profile string) {
	b.mu.Lock()
	dayDir := b.dayDir
	tabs := append([]*liveTab(nil), b.tabs...)
	cur := ""
	if b.current >= 0 && b.current < len(b.tabs) {
		cur = b.tabs[b.current].url
	}
	cmd := b.cmd
	ws := b.ws
	b.cmd = nil
	b.ws = nil
	b.mu.Unlock()

	if dayDir != "" {
		_ = writeTabs(dayDir, cur, tabs)
	}
	if ws != nil {
		_ = ws.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() {
			_, _ = cmd.Process.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			_ = cmd.Process.Kill()
		}
	}
	if dayDir != "" && profile != "" {
		_ = connectors.MirrorCookies(dayDir, profile)
	}
}

func writeTabs(dayDir, current string, tabs []*liveTab) error {
	var saved savedBrowser
	saved.Current = current
	for _, t := range tabs {
		if t == nil || t.url == "" || t.url == "about:blank" {
			continue
		}
		saved.Tabs = append(saved.Tabs, savedTab{URL: t.url, Title: t.title})
		if current == "" {
			saved.Current = t.url
			current = t.url
		}
	}
	if len(saved.Tabs) == 0 {
		_ = os.Remove(filepath.Join(dayDir, "tabs.json"))
		return nil
	}
	raw, err := json.MarshalIndent(saved, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dayDir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dayDir, "tabs.json"), raw, 0600)
}

func readTabs(dayDir string) savedBrowser {
	var saved savedBrowser
	raw, err := os.ReadFile(filepath.Join(dayDir, "tabs.json"))
	if err != nil {
		return saved
	}
	_ = json.Unmarshal(raw, &saved)
	return saved
}

func browserStateNote(private bool, chatID, userID int64) string {
	_, _, dayDir, day := browserKey(private, chatID, userID)
	saved := readTabs(dayDir)
	who := "this user's private browser"
	if !private {
		who = "this group's shared browser"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n<browser_session day=%q scope=%q>\n", day, who)
	if len(saved.Tabs) == 0 {
		b.WriteString("No tabs yet today. The first browser tool starts the session and later tools in this turn keep the same tabs.\n")
	} else {
		b.WriteString("Tabs from the last close. The next browser tool reopens them and further tools in this turn stay on that session. Omit url to use the current tab.\n")
		for _, t := range saved.Tabs {
			cur := ""
			if t.URL == saved.Current {
				cur = ` current="true"`
			}
			fmt.Fprintf(&b, "<btab%s url=%q>%s</btab>\n", cur, xmlEscape(t.URL), xmlEscape(t.Title))
		}
	}
	b.WriteString("</browser_session>")
	return b.String()
}

func ensureBrowser(private bool, chatID, userID int64) (*liveBrowser, error) {
	key, profile, dayDir, day := browserKey(private, chatID, userID)
	browsersMu.Lock()
	b := browsers[key]
	if b == nil {
		b = &liveBrowser{}
		browsers[key] = b
	}
	browsersMu.Unlock()

	b.startMu.Lock()
	defer b.startMu.Unlock()

	b.mu.Lock()
	same := b.ws != nil && b.day == day
	stale := b.cmd != nil && b.day != "" && b.day != day
	b.mu.Unlock()
	if same {
		return b, nil
	}
	if stale {
		b.shutdown(profile)
	}
	if err := os.MkdirAll(dayDir, 0700); err != nil {
		return nil, err
	}
	_ = connectors.SeedCookies(profile, dayDir)
	if err := b.start(profile, dayDir, day); err != nil {
		return nil, err
	}
	saved := readTabs(dayDir)
	for _, t := range saved.Tabs {
		if _, err := b.open(t.URL); err != nil {
			return nil, err
		}
	}
	b.mu.Lock()
	if saved.Current != "" {
		for i, t := range b.tabs {
			if t.url == saved.Current {
				b.current = i
				break
			}
		}
	}
	b.mu.Unlock()
	return b, nil
}

func (b *liveBrowser) start(profile, dayDir, day string) error {
	bin, err := exec.LookPath("obscura")
	if err != nil {
		return fmt.Errorf("obscura is not installed")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	token := randomToken()
	cmd := exec.Command(bin, "--stealth", "--storage-dir", dayDir, "serve", "--host", "127.0.0.1", "--port", fmt.Sprint(port), "--quiet")
	cmd.Env = append(os.Environ(), "OBSCURA_CDP_TOKEN="+token)
	if err := cmd.Start(); err != nil {
		return err
	}
	b.cmd = cmd
	b.port = port
	b.token = token
	b.profile = profile
	b.dayDir = dayDir
	b.day = day
	b.pending = map[int]chan cdpMsg{}
	b.tabs = nil
	b.current = 0

	deadline := time.Now().Add(3 * time.Second)
	var ready bool
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/json/version", port), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				ready = true
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
	if !ready {
		_ = cmd.Process.Kill()
		b.cmd = nil
		return fmt.Errorf("obscura did not start")
	}
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+token)
	ws, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d/devtools/browser", port), hdr)
	if err != nil {
		_ = cmd.Process.Kill()
		b.cmd = nil
		return err
	}
	b.ws = ws
	go b.readLoop()
	return nil
}

func (b *liveBrowser) readLoop() {
	for {
		var msg cdpMsg
		if err := b.ws.ReadJSON(&msg); err != nil {
			return
		}
		if msg.ID == 0 {
			continue
		}
		b.mu.Lock()
		ch := b.pending[msg.ID]
		delete(b.pending, msg.ID)
		b.mu.Unlock()
		if ch != nil {
			ch <- msg
		}
	}
}

func (b *liveBrowser) call(ctx context.Context, method string, params any, session string) (json.RawMessage, error) {
	b.mu.Lock()
	if b.ws == nil || b.pending == nil {
		b.mu.Unlock()
		return nil, fmt.Errorf("browser is closed")
	}
	b.nextID++
	id := b.nextID
	ws := b.ws
	ch := make(chan cdpMsg, 1)
	b.pending[id] = ch
	b.mu.Unlock()

	payload := map[string]any{"id": id, "method": method, "params": params}
	if session != "" {
		payload["sessionId"] = session
	}
	if err := ws.WriteJSON(payload); err != nil {
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return nil, err
	}
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case msg := <-ch:
		if msg.Error != nil {
			return nil, fmt.Errorf("%s", msg.Error.Message)
		}
		return msg.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("%s timed out", method)
	}
}

func (b *liveBrowser) open(rawURL string) (*liveTab, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	raw, err := b.call(ctx, "Target.createTarget", map[string]any{"url": rawURL}, "")
	if err != nil {
		return nil, err
	}
	var created struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return nil, err
	}
	raw, err = b.call(ctx, "Target.attachToTarget", map[string]any{"targetId": created.TargetID, "flatten": true}, "")
	if err != nil {
		return nil, err
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &attached); err != nil {
		return nil, err
	}
	tab := &liveTab{id: created.TargetID, session: attached.SessionID, url: rawURL}
	_ = b.waitReady(ctx, tab)
	b.refresh(ctx, tab)
	b.mu.Lock()
	b.tabs = append(b.tabs, tab)
	b.current = len(b.tabs) - 1
	b.mu.Unlock()
	return tab, nil
}

func (b *liveBrowser) waitReady(ctx context.Context, tab *liveTab) error {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := b.call(ctx, "Runtime.evaluate", map[string]any{
			"expression":    "document.readyState",
			"returnByValue": true,
		}, tab.session)
		if err == nil && strings.Contains(string(raw), "complete") {
			return nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil
}

func (b *liveBrowser) refresh(ctx context.Context, tab *liveTab) {
	raw, err := b.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "JSON.stringify({url:location.href,title:document.title||''})",
		"returnByValue": true,
	}, tab.session)
	if err != nil {
		return
	}
	val := cdpString(raw)
	var info struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	}
	if json.Unmarshal([]byte(val), &info) == nil {
		if info.URL != "" {
			tab.url = info.URL
		}
		tab.title = info.Title
	}
}

func (b *liveBrowser) cur() (*liveTab, error) {
	if len(b.tabs) == 0 {
		return nil, fmt.Errorf("no tab yet. pass url or action newtab")
	}
	if b.current < 0 || b.current >= len(b.tabs) {
		b.current = 0
	}
	return b.tabs[b.current], nil
}

func (b *liveBrowser) navigate(ctx context.Context, tab *liveTab, rawURL string) error {
	if _, err := b.call(ctx, "Page.navigate", map[string]any{"url": rawURL}, tab.session); err != nil {
		return err
	}
	tab.url = rawURL
	return b.waitReady(ctx, tab)
}

func (b *liveBrowser) snapshot() []map[string]any {
	out := []map[string]any{}
	for i, t := range b.tabs {
		out = append(out, map[string]any{
			"index":   i + 1,
			"url":     t.url,
			"title":   t.title,
			"current": i == b.current,
		})
	}
	return out
}

func cdpString(raw json.RawMessage) string {
	var wrap struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &wrap) != nil || wrap.Result.Value == nil {
		return ""
	}
	switch v := wrap.Result.Value.(type) {
	case string:
		return v
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func randomToken() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
