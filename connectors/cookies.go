package connectors

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Cookie is the Obscura cookies.json shape.
type Cookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Secure   bool   `json:"secure"`
	HTTPOnly bool   `json:"httpOnly"`
	SameSite string `json:"sameSite,omitempty"`
	Expires  *int64 `json:"expires,omitempty"`
	HostOnly *bool  `json:"hostOnly,omitempty"`
}

func profilesRoot() string {
	root := os.Getenv("PROFILES_DIR")
	if root == "" {
		root = "/app/data/profiles"
	}
	return root
}

func ProfileDir(userID int64) string {
	return filepath.Join(profilesRoot(), strconv.FormatInt(userID, 10))
}

// GroupProfileDir is the shared cookie profile for a group chat.
func GroupProfileDir(chatID int64) string {
	return filepath.Join(profilesRoot(), fmt.Sprintf("chat-%d", chatID))
}

// ScopeProfile is the cookie home for a private user or a shared group browser.
func ScopeProfile(private bool, chatID, userID int64) string {
	if private {
		return ProfileDir(userID)
	}
	return GroupProfileDir(chatID)
}

func cookiesJSONPath(userID int64) string {
	return filepath.Join(ProfileDir(userID), "cookies.json")
}

func cookiesTxtPath(userID int64) string {
	return filepath.Join(ProfileDir(userID), "cookies.txt")
}

// NetscapeCookies is the yt-dlp cookie file, or empty when the user has none.
func NetscapeCookies(userID int64) string {
	p := cookiesTxtPath(userID)
	if st, err := os.Stat(p); err == nil && st.Size() > 0 {
		return p
	}
	return ""
}

// CookieCount is how many cookies are stored for the user.
func CookieCount(userID int64) int {
	list, err := readCookies(userID)
	if err != nil {
		return 0
	}
	return len(list)
}

// ImportCookiesDir writes cookies into dir (cookies.json and cookies.txt).
func ImportCookiesDir(dir string, raw []byte) (int, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 0, fmt.Errorf("empty cookies")
	}
	var list []Cookie
	var err error
	if raw[0] == '[' || raw[0] == '{' {
		list, err = parseJSONCookies(raw)
	} else {
		list, err = parseNetscape(raw)
	}
	if err != nil {
		return 0, err
	}
	if len(list) == 0 {
		return 0, fmt.Errorf("no cookies found in that file")
	}
	if err := writeCookiesTo(dir, list); err != nil {
		return 0, err
	}
	return len(list), nil
}

// ImportCookies accepts a Netscape cookies.txt or a JSON cookie export and
// writes both Obscura's cookies.json and a Netscape file for yt-dlp.
func ImportCookies(userID int64, raw []byte) (int, error) {
	return ImportCookiesDir(ProfileDir(userID), raw)
}

func cookieCountDir(dir string) int {
	data, err := os.ReadFile(filepath.Join(dir, "cookies.json"))
	if err != nil {
		return 0
	}
	list, err := parseJSONCookies(data)
	if err != nil {
		return 0
	}
	return len(list)
}

// CookieCountDir is how many cookies are stored in dir.
func CookieCountDir(dir string) int { return cookieCountDir(dir) }

// NetscapeIn returns cookies.txt in dir when it exists.
func NetscapeIn(dir string) string {
	p := filepath.Join(dir, "cookies.txt")
	if st, err := os.Stat(p); err == nil && st.Size() > 0 {
		return p
	}
	return ""
}

// SeedCookies copies the saved jar into a fresh day directory.
func SeedCookies(profile, dayDir string) error {
	dst := filepath.Join(dayDir, "cookies.json")
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(profile, "cookies.json"))
	if err != nil || len(data) == 0 {
		return nil
	}
	if err := os.MkdirAll(dayDir, 0700); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}

// MirrorCookies copies a day jar back to the lasting profile and refreshes cookies.txt.
func MirrorCookies(dayDir, profile string) error {
	data, err := os.ReadFile(filepath.Join(dayDir, "cookies.json"))
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	list, err := parseJSONCookies(data)
	if err != nil || len(list) == 0 {
		return err
	}
	return writeCookiesTo(profile, list)
}

// ClearCookies deletes the profile cookie files.
func ClearCookies(userID int64) error {
	dir := ProfileDir(userID)
	_ = os.Remove(filepath.Join(dir, "cookies.json"))
	_ = os.Remove(filepath.Join(dir, "cookies.txt"))
	return nil
}

// SyncNetscape rewrites cookies.txt from cookies.json after Obscura saves.
func SyncNetscape(userID int64) {
	list, err := readCookies(userID)
	if err != nil || len(list) == 0 {
		return
	}
	_ = writeNetscape(userID, list)
}

func readCookies(userID int64) ([]Cookie, error) {
	data, err := os.ReadFile(cookiesJSONPath(userID))
	if err != nil {
		return nil, err
	}
	return parseJSONCookies(data)
}

func writeCookies(userID int64, list []Cookie) error {
	return writeCookiesTo(ProfileDir(userID), list)
}

func writeCookiesTo(dir string, list []Cookie) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "cookies.json"), raw, 0600); err != nil {
		return err
	}
	return writeNetscapeFile(filepath.Join(dir, "cookies.txt"), list)
}

func writeNetscape(userID int64, list []Cookie) error {
	return writeNetscapeFile(cookiesTxtPath(userID), list)
}

func writeNetscapeFile(path string, list []Cookie) error {
	var b strings.Builder
	b.WriteString("# Netscape HTTP Cookie File\n")
	for _, c := range list {
		domain := c.Domain
		include := "FALSE"
		if c.HostOnly == nil || !*c.HostOnly {
			include = "TRUE"
			if domain != "" && !strings.HasPrefix(domain, ".") {
				domain = "." + domain
			}
		}
		secure := "FALSE"
		if c.Secure {
			secure = "TRUE"
		}
		exp := "0"
		if c.Expires != nil {
			exp = strconv.FormatInt(*c.Expires, 10)
		}
		cpath := c.Path
		if cpath == "" {
			cpath = "/"
		}
		name := c.Name
		if c.HTTPOnly {
			domain = "#HttpOnly_" + domain
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", domain, include, cpath, secure, exp, name, c.Value)
	}
	return os.WriteFile(path, []byte(b.String()), 0600)
}

func parseJSONCookies(raw []byte) ([]Cookie, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '{' {
		var wrap struct {
			Cookies []map[string]any `json:"cookies"`
		}
		if err := json.Unmarshal(raw, &wrap); err == nil && len(wrap.Cookies) > 0 {
			return mapsToCookies(wrap.Cookies), nil
		}
		var one map[string]any
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, err
		}
		return mapsToCookies([]map[string]any{one}), nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	return mapsToCookies(rows), nil
}

func mapsToCookies(rows []map[string]any) []Cookie {
	var out []Cookie
	for _, m := range rows {
		name, _ := m["name"].(string)
		value, _ := m["value"].(string)
		domain, _ := m["domain"].(string)
		if name == "" || domain == "" {
			continue
		}
		c := Cookie{
			Name:     name,
			Value:    value,
			Domain:   strings.TrimPrefix(domain, "#HttpOnly_"),
			Path:     stringField(m, "path", "/"),
			Secure:   boolField(m, "secure"),
			HTTPOnly: boolField(m, "httpOnly") || boolField(m, "http_only"),
			SameSite: stringField(m, "sameSite", "Lax"),
		}
		if exp, ok := intField(m, "expires"); ok {
			c.Expires = &exp
		} else if exp, ok := intField(m, "expirationDate"); ok {
			c.Expires = &exp
		}
		hostOnly := !strings.HasPrefix(domain, ".")
		if v, ok := m["hostOnly"].(bool); ok {
			hostOnly = v
		}
		c.HostOnly = &hostOnly
		if c.SameSite == "" {
			c.SameSite = "Lax"
		}
		out = append(out, c)
	}
	return out
}

func parseNetscape(raw []byte) ([]Cookie, error) {
	var out []Cookie
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || (strings.HasPrefix(line, "#") && !strings.HasPrefix(line, "#HttpOnly_")) {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			continue
		}
		domain := parts[0]
		httpOnly := strings.HasPrefix(domain, "#HttpOnly_")
		domain = strings.TrimPrefix(domain, "#HttpOnly_")
		hostOnly := strings.EqualFold(parts[1], "FALSE")
		secure := strings.EqualFold(parts[3], "TRUE")
		var exp *int64
		if n, err := strconv.ParseInt(parts[4], 10, 64); err == nil && n > 0 {
			exp = &n
		}
		c := Cookie{
			Name:     parts[5],
			Value:    parts[6],
			Domain:   domain,
			Path:     parts[2],
			Secure:   secure,
			HTTPOnly: httpOnly,
			SameSite: "Lax",
			Expires:  exp,
			HostOnly: &hostOnly,
		}
		out = append(out, c)
	}
	return out, sc.Err()
}

// ApplyCookies sets a Cookie header for rawURL from the user's profile.
func ApplyCookies(userID int64, rawURL string, header http.Header) {
	list, err := readCookies(userID)
	if err != nil || len(list) == 0 {
		return
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	host := strings.ToLower(u.Hostname())
	now := time.Now().Unix()
	var pairs []string
	for _, c := range list {
		if c.Expires != nil && *c.Expires > 0 && *c.Expires < now {
			continue
		}
		if c.Secure && u.Scheme != "https" {
			continue
		}
		if !domainMatch(host, c) {
			continue
		}
		pairs = append(pairs, c.Name+"="+c.Value)
	}
	if len(pairs) > 0 {
		header.Set("Cookie", strings.Join(pairs, "; "))
	}
}

func domainMatch(host string, c Cookie) bool {
	d := strings.ToLower(strings.TrimPrefix(c.Domain, "."))
	if c.HostOnly != nil && *c.HostOnly {
		return host == d
	}
	return host == d || strings.HasSuffix(host, "."+d)
}

func stringField(m map[string]any, key, fallback string) string {
	if s, ok := m[key].(string); ok && s != "" {
		return s
	}
	return fallback
}

func boolField(m map[string]any, key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	switch n := v.(type) {
	case bool:
		return n
	case string:
		return strings.EqualFold(n, "true")
	default:
		return false
	}
}

func intField(m map[string]any, key string) (int64, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}
