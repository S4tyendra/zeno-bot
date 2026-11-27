package aichat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"google.golang.org/genai"

	"zeno/connectors"
)

const pageExtractJS = `(function(){
  const text = (document.body && document.body.innerText || "").replace(/\n{3,}/g, "\n\n").slice(0, 7000);
  const nodes = Array.from(document.querySelectorAll("a, button, input[type=submit], [role=button]")).slice(0, 40);
  const buttons = nodes.map(function(el){
    return {
      tag: el.tagName,
      text: ((el.innerText || el.value || el.getAttribute("aria-label") || "") + "").trim().slice(0, 80),
      href: el.href || ""
    };
  }).filter(function(x){ return x.text || x.href; });
  return JSON.stringify({title: document.title || "", url: location.href, text: text, buttons: buttons});
})()`

func executeBrowser(args map[string]any, chatID, userID int64, private bool) map[string]any {
	if userID == 0 && private {
		return map[string]any{"success": false, "error": "missing user"}
	}
	action := strings.ToLower(argString(args, "action"))
	rawURL := argString(args, "url")
	if action == "search" {
		q := argString(args, "query")
		if q == "" {
			return map[string]any{"success": false, "error": "query is required"}
		}
		rawURL = "https://duckduckgo.com/?q=" + url.QueryEscape(q)
		action = "text"
	}
	if action == "download" {
		if rawURL == "" || !strings.HasPrefix(rawURL, "http") {
			return map[string]any{"success": false, "error": "url is required"}
		}
		return browserDownload(args, chatID, userID, rawURL)
	}
	switch action {
	case "text", "screenshot", "scroll", "click", "state", "newtab", "switch", "close":
	default:
		return map[string]any{"success": false, "error": "action must be state, text, screenshot, scroll, click, search, newtab, switch, close, or download"}
	}

	b, err := ensureBrowser(private, chatID, userID)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	b.startMu.Lock()
	defer b.startMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	switch action {
	case "state":
		b.refreshAll(ctx)
		return map[string]any{"success": true, "tabs": b.snapshot()}
	case "newtab":
		if rawURL == "" {
			rawURL = "about:blank"
		}
		tab, err := b.open(rawURL)
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}
		}
		return map[string]any{"success": true, "url": tab.url, "title": tab.title, "tabs": b.snapshot()}
	case "switch":
		if err := b.switchTab(args); err != nil {
			return map[string]any{"success": false, "error": err.Error(), "tabs": b.snapshot()}
		}
		tab, _ := b.cur()
		return map[string]any{"success": true, "url": tab.url, "title": tab.title, "tabs": b.snapshot()}
	case "close":
		if err := b.closeTab(ctx); err != nil {
			return map[string]any{"success": false, "error": err.Error()}
		}
		return map[string]any{"success": true, "tabs": b.snapshot()}
	}

	tab, err := b.prepare(ctx, rawURL, action == "text" || action == "screenshot" || action == "scroll" || action == "click")
	if err != nil {
		return map[string]any{"success": false, "error": err.Error(), "tabs": b.snapshot()}
	}
	if w, ok := argInt64(args, "wait"); ok && w > 0 {
		if w > 10 {
			w = 10
		}
		timer := time.NewTimer(time.Duration(w) * time.Second)
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		timer.Stop()
	}

	switch action {
	case "screenshot":
		return b.shot(ctx, tab)
	case "scroll":
		px := 900
		if n, ok := argInt64(args, "pixels", "dy"); ok && n != 0 {
			px = int(n)
		}
		js := fmt.Sprintf(`(function(){window.scrollBy(0,%d);return JSON.stringify({url:location.href,scrollY:window.scrollY,text:(document.body&&document.body.innerText||"").slice(0,7000)});})()`, px)
		out, err := b.eval(ctx, tab, js)
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}
		}
		b.refresh(ctx, tab)
		return map[string]any{"success": true, "page": trimOut(out), "tabs": b.snapshot()}
	case "click":
		target := argString(args, "target", "selector", "text")
		if target == "" {
			return map[string]any{"success": false, "error": "target is required (css selector or button text)"}
		}
		js := fmt.Sprintf(`(function(){
		  const want = %s;
		  let el = null;
		  try { el = document.querySelector(want); } catch (e) {}
		  if (!el) {
		    const nodes = Array.from(document.querySelectorAll("a, button, [role=button], input[type=submit]"));
		    el = nodes.find(function(n){ return (((n.innerText||n.value||"")+"").toLowerCase().indexOf(want.toLowerCase()) >= 0); });
		  }
		  if (!el) return JSON.stringify({error:"not found"});
		  el.click();
		  return JSON.stringify({clicked: ((el.innerText||el.href||el.value||"")+"").slice(0,160)});
		})()`, strconv.Quote(target))
		clicked, err := b.eval(ctx, tab, js)
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}
		}
		time.Sleep(700 * time.Millisecond)
		b.refresh(ctx, tab)
		page, _ := b.eval(ctx, tab, pageExtractJS)
		return map[string]any{"success": true, "click": trimOut(clicked), "page": trimOut(page), "tabs": b.snapshot()}
	default:
		page, err := b.eval(ctx, tab, pageExtractJS)
		if err != nil {
			return map[string]any{"success": false, "error": err.Error()}
		}
		b.refresh(ctx, tab)
		return map[string]any{"success": true, "page": trimOut(page), "tabs": b.snapshot()}
	}
}

func (b *liveBrowser) prepare(ctx context.Context, rawURL string, allowCurrent bool) (*liveTab, error) {
	if rawURL != "" {
		if !strings.HasPrefix(rawURL, "http") {
			return nil, fmt.Errorf("url must start with http")
		}
		tab, err := b.cur()
		if err != nil {
			return b.open(rawURL)
		}
		if err := b.navigate(ctx, tab, rawURL); err != nil {
			return nil, err
		}
		b.refresh(ctx, tab)
		return tab, nil
	}
	if !allowCurrent {
		return nil, fmt.Errorf("url is required")
	}
	return b.cur()
}

func (b *liveBrowser) eval(ctx context.Context, tab *liveTab, js string) (string, error) {
	raw, err := b.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    js,
		"returnByValue": true,
	}, tab.session)
	if err != nil {
		return "", err
	}
	return cdpString(raw), nil
}

func (b *liveBrowser) shot(ctx context.Context, tab *liveTab) map[string]any {
	raw, err := b.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png"}, tab.session)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	var shot struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(raw, &shot); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	data, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	if err := os.MkdirAll(GeneratedImagesDir, 0755); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	outPath := filepath.Join(GeneratedImagesDir, fmt.Sprintf("page_%d.png", time.Now().UnixNano()))
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		return map[string]any{"success": false, "error": err.Error()}
	}
	b.refresh(ctx, tab)
	res := map[string]any{"success": true, "url": tab.url, "file_path": outPath, "bytes": len(data), "tabs": b.snapshot()}
	if len(data) > 0 && len(data) < 8<<20 {
		res["_attach"] = []*genai.Part{{
			InlineData: &genai.Blob{MIMEType: "image/png", Data: data},
		}}
	}
	return res
}

func (b *liveBrowser) switchTab(args map[string]any) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n, ok := argInt64(args, "index"); ok && n >= 1 && int(n) <= len(b.tabs) {
		b.current = int(n) - 1
		return nil
	}
	q := strings.ToLower(argString(args, "url", "target", "query"))
	if q == "" {
		return fmt.Errorf("pass index or a url fragment")
	}
	for i, t := range b.tabs {
		if strings.Contains(strings.ToLower(t.url), q) || strings.Contains(strings.ToLower(t.title), q) {
			b.current = i
			return nil
		}
	}
	return fmt.Errorf("no tab matches")
}

func (b *liveBrowser) closeTab(ctx context.Context) error {
	tab, err := b.cur()
	if err != nil {
		return err
	}
	if _, err := b.call(ctx, "Target.closeTarget", map[string]any{"targetId": tab.id}, ""); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	next := []*liveTab{}
	for _, t := range b.tabs {
		if t.id != tab.id {
			next = append(next, t)
		}
	}
	b.tabs = next
	if b.current >= len(b.tabs) {
		b.current = len(b.tabs) - 1
	}
	if b.current < 0 {
		b.current = 0
	}
	return nil
}

func (b *liveBrowser) refreshAll(ctx context.Context) {
	b.mu.Lock()
	tabs := append([]*liveTab(nil), b.tabs...)
	b.mu.Unlock()
	for _, t := range tabs {
		b.refresh(ctx, t)
	}
}

func browserDownload(args map[string]any, chatID, userID int64, rawURL string) map[string]any {
	name := argString(args, "name", "filename")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	status := startProgress(chatID, "⬇️ Uploading to your bucket")
	link, size, err := connectors.SaveRemote(ctx, userID, rawURL, name, func(written, total int64) {
		status.tick(written, total)
	})
	if err != nil {
		status.done("❌ " + err.Error())
		return map[string]any{"success": false, "error": err.Error()}
	}
	status.done("✅ " + link)
	return map[string]any{"success": true, "url": link, "bytes": size, "source": rawURL}
}

func trimOut(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 8000 {
		return s[:8000]
	}
	return s
}

type progressNote struct {
	chatID int64
	msgID  int32
	last   time.Time
	title  string
}

func startProgress(chatID int64, title string) *progressNote {
	p := &progressNote{chatID: chatID, title: title}
	if botClient == nil || chatID == 0 {
		return p
	}
	msg, err := botClient.SendMessage(chatID, title+"…")
	if err == nil && msg != nil {
		p.msgID = msg.ID
	}
	return p
}

func (p *progressNote) tick(written, total int64) {
	if p.msgID == 0 || time.Since(p.last) < 2*time.Second {
		return
	}
	p.last = time.Now()
	text := p.title
	if total > 0 {
		text = fmt.Sprintf("%s\n%d / %d bytes (%.0f%%)", p.title, written, total, float64(written)/float64(total)*100)
	} else if written > 0 {
		text = fmt.Sprintf("%s\n%d bytes", p.title, written)
	}
	_, _ = botClient.EditMessage(p.chatID, p.msgID, text)
}

func (p *progressNote) line(s string) {
	if p.msgID == 0 || time.Since(p.last) < 2*time.Second {
		return
	}
	p.last = time.Now()
	if len(s) > 180 {
		s = s[:180]
	}
	_, _ = botClient.EditMessage(p.chatID, p.msgID, p.title+"\n"+s)
}

func (p *progressNote) done(s string) {
	if p.msgID == 0 {
		return
	}
	if len(s) > 3500 {
		s = s[:3500]
	}
	_, _ = botClient.EditMessage(p.chatID, p.msgID, s)
}
