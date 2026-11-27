package connectors

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNetscapeRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROFILES_DIR", dir)
	raw := []byte("# Netscape HTTP Cookie File\n.instagram.com\tTRUE\t/\tTRUE\t2000000000\tsessionid\tabc\n")
	n, err := ImportCookies(42, raw)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("count %d", n)
	}
	if NetscapeCookies(42) == "" {
		t.Fatal("missing netscape file")
	}
	txt, err := os.ReadFile(filepath.Join(dir, "42", "cookies.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(txt), "sessionid") {
		t.Fatalf("txt %s", txt)
	}
	js, err := os.ReadFile(filepath.Join(dir, "42", "cookies.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), `"httpOnly"`) {
		t.Fatalf("json %s", js)
	}
}

func TestJSONCookies(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROFILES_DIR", dir)
	raw := []byte(`[{"name":"sid","value":"1","domain":".example.com","path":"/","secure":true,"expirationDate":2000000000}]`)
	n, err := ImportCookies(7, raw)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || CookieCount(7) != 1 {
		t.Fatalf("n=%d count=%d", n, CookieCount(7))
	}
}

func TestParseKV(t *testing.T) {
	kv := ParseKV("endpoint=https://s3.amazonaws.com\nsecret_key=abc=def\n")
	if kv["endpoint"] != "https://s3.amazonaws.com" || kv["secret_key"] != "abc=def" {
		t.Fatalf("%v", kv)
	}
}

func TestSplitEndpoint(t *testing.T) {
	host, secure, err := splitEndpoint("s3.amazonaws.com")
	if err != nil || host != "s3.amazonaws.com" || !secure {
		t.Fatalf("%s %v %v", host, secure, err)
	}
	host, secure, err = splitEndpoint("http://127.0.0.1:9000")
	if err != nil || host != "127.0.0.1:9000" || secure {
		t.Fatalf("%s %v %v", host, secure, err)
	}
}
