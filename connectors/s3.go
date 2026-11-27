package connectors

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"zeno/db"
)

// presignStream uploads a known-length body with one presigned PUT.
func (c Conn) presignStream(ctx context.Context, name, contentType string, r io.Reader, size int64, progress func(written, total int64)) (string, error) {
	key := c.objectKey(name)
	if err := c.presignedPut(ctx, key, contentType, r, size, progress); err != nil {
		return "", err
	}
	return c.LinkFor(ctx, key)
}

// PutSDK uploads with the S3 multipart API. size may be -1.
func (c Conn) PutSDK(ctx context.Context, name, contentType string, r io.Reader, size int64, progress func(written, total int64)) (string, error) {
	key := c.objectKey(name)
	cl, err := c.client()
	if err != nil {
		return "", err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err = cl.PutObject(ctx, c.Bucket, key, &progressReader{r: r, total: size, cb: progress}, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", err
	}
	return c.LinkFor(ctx, key)
}

// ErrNoConnector means this user has not saved an S3 connector.
var ErrNoConnector = errors.New("no S3 connector for this user — set one with /s3")

// Conn is one user's S3-compatible bucket.
type Conn struct {
	UserID     int64
	Endpoint   string
	Bucket     string
	Region     string
	AccessKey  string
	SecretKey  string
	Prefix     string
	PublicBase string
}

// Load returns the connector for userID.
func Load(ctx context.Context, userID int64) (Conn, error) {
	var c Conn
	if db.Pool == nil || userID == 0 {
		return c, ErrNoConnector
	}
	err := db.Pool.QueryRow(ctx, `
		SELECT user_id, endpoint, bucket, region, access_key, secret_key, prefix, public_base
		FROM user_s3 WHERE user_id = $1`, userID).Scan(
		&c.UserID, &c.Endpoint, &c.Bucket, &c.Region, &c.AccessKey, &c.SecretKey, &c.Prefix, &c.PublicBase,
	)
	if err != nil {
		return Conn{}, ErrNoConnector
	}
	return c, nil
}

// Save upserts a connector.
func Save(ctx context.Context, c Conn) error {
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	_, err := db.Pool.Exec(ctx, `
		INSERT INTO user_s3 (user_id, endpoint, bucket, region, access_key, secret_key, prefix, public_base, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			endpoint = EXCLUDED.endpoint,
			bucket = EXCLUDED.bucket,
			region = EXCLUDED.region,
			access_key = EXCLUDED.access_key,
			secret_key = EXCLUDED.secret_key,
			prefix = EXCLUDED.prefix,
			public_base = EXCLUDED.public_base,
			updated_at = NOW()`,
		c.UserID, c.Endpoint, c.Bucket, c.Region, c.AccessKey, c.SecretKey, c.Prefix, c.PublicBase)
	return err
}

// Clear removes a connector.
func Clear(ctx context.Context, userID int64) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM user_s3 WHERE user_id = $1`, userID)
	return err
}

// PromptNote is safe to put in the system prompt. It has no keys.
func PromptNote(userID int64) string {
	c, err := Load(context.Background(), userID)
	cookies := CookieCount(userID)
	var b strings.Builder
	b.WriteString("\n\n<user_connector>\n")
	if err != nil {
		b.WriteString("This user has no S3 connector. /download, /link, copylink, and browser download need one. Tell them to send /s3 with endpoint, bucket, access_key, secret_key.\n")
	} else {
		fmt.Fprintf(&b, "This user has an S3 bucket %q at %q (region %s, prefix %q). Put files there with copylink, ytdlp, or browser download. Do not ask for keys.\n", c.Bucket, c.Endpoint, c.Region, c.Prefix)
	}
	if cookies > 0 {
		fmt.Fprintf(&b, "Browser profile has %d cookies. browser and ytdlp use them.\n", cookies)
	} else {
		b.WriteString("No cookies saved. Logged-in pages and Instagram downloads need /cookies (Netscape cookies.txt or a JSON export).\n")
	}
	b.WriteString("</user_connector>")
	return b.String()
}

func (c Conn) client() (*minio.Client, error) {
	host, secure, err := splitEndpoint(c.Endpoint)
	if err != nil {
		return nil, err
	}
	region := c.Region
	if region == "" {
		region = "us-east-1"
	}
	return minio.New(host, &minio.Options{
		Creds:        credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""),
		Secure:       secure,
		Region:       region,
		BucketLookup: minio.BucketLookupAuto,
	})
}

func splitEndpoint(raw string) (host string, secure bool, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, fmt.Errorf("endpoint is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false, fmt.Errorf("bad endpoint %q", raw)
	}
	return u.Host, u.Scheme != "http", nil
}

func (c Conn) objectKey(name string) string {
	name = SafeName(name)
	prefix := strings.Trim(c.Prefix, "/")
	stamp := time.Now().UTC().Format("2006/01/02")
	id := fmt.Sprintf("%d_%s", time.Now().UnixNano(), name)
	if prefix == "" {
		return stamp + "/" + id
	}
	return prefix + "/" + stamp + "/" + id
}

// LinkFor returns a public URL or a 7-day presigned GET.
func (c Conn) LinkFor(ctx context.Context, key string) (string, error) {
	if base := strings.TrimRight(strings.TrimSpace(c.PublicBase), "/"); base != "" {
		return base + "/" + escapeKey(key), nil
	}
	cl, err := c.client()
	if err != nil {
		return "", err
	}
	u, err := cl.PresignedGetObject(ctx, c.Bucket, key, 7*24*time.Hour, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func escapeKey(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// Put uploads r. size < 0 uses multipart. A known size is sent with a presigned PUT first.
func (c Conn) Put(ctx context.Context, name, contentType string, r io.Reader, size int64, progress func(written, total int64)) (string, error) {
	key := c.objectKey(name)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if size >= 0 {
		if err := c.presignedPut(ctx, key, contentType, r, size, progress); err == nil {
			return c.LinkFor(ctx, key)
		}
		// The body was consumed. Caller retries with a fresh reader when that matters.
		return "", fmt.Errorf("presigned put failed and the body was already read")
	}
	cl, err := c.client()
	if err != nil {
		return "", err
	}
	_, err = cl.PutObject(ctx, c.Bucket, key, &progressReader{r: r, total: size, cb: progress}, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", err
	}
	return c.LinkFor(ctx, key)
}

func (c Conn) presignedPut(ctx context.Context, key, contentType string, r io.Reader, size int64, progress func(written, total int64)) error {
	cl, err := c.client()
	if err != nil {
		return err
	}
	u, err := cl.PresignedPutObject(ctx, c.Bucket, key, 2*time.Hour)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), &progressReader{r: r, total: size, cb: progress})
	if err != nil {
		return err
	}
	req.ContentLength = size
	if contentType != "" {
		// Content-Type is not part of this presign. Leave it unset so the signature matches.
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("presigned PUT %s: %s", resp.Status, strings.TrimSpace(string(snippet)))
	}
	return nil
}

// PutFile uploads a local file and returns the link.
func PutFile(ctx context.Context, userID int64, filePath, name string) (string, int64, error) {
	c, err := Load(ctx, userID)
	if err != nil {
		return "", 0, err
	}
	f, err := openFile(filePath)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	if name == "" {
		name = path.Base(filePath)
	}
	link, err := c.putKnown(ctx, name, "", f, st.Size(), nil)
	return link, st.Size(), err
}

// putKnown presigns when it can, and falls back to the SDK with the same reader only if presign fails before reading.
func (c Conn) putKnown(ctx context.Context, name, contentType string, r io.ReadSeeker, size int64, progress func(written, total int64)) (string, error) {
	key := c.objectKey(name)
	if err := c.presignedPut(ctx, key, contentType, r, size, progress); err == nil {
		return c.LinkFor(ctx, key)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	cl, err := c.client()
	if err != nil {
		return "", err
	}
	_, err = cl.PutObject(ctx, c.Bucket, key, &progressReader{r: r, total: size, cb: progress}, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return "", err
	}
	return c.LinkFor(ctx, key)
}

type progressReader struct {
	r     io.Reader
	n     int64
	total int64
	cb    func(written, total int64)
	last  time.Time
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.n += int64(n)
		if p.cb != nil && time.Since(p.last) > time.Second {
			p.cb(p.n, p.total)
			p.last = time.Now()
		}
	}
	if err == io.EOF && p.cb != nil {
		p.cb(p.n, p.total)
	}
	return n, err
}

// PutFileTemp uploads a local file and returns a presigned GET link that expires after ttl.
// It ignores public_base so the link always expires.
func PutFileTemp(ctx context.Context, userID int64, filePath, name string, ttl time.Duration) (string, error) {
	c, err := Load(ctx, userID)
	if err != nil {
		return "", err
	}
	f, err := openFile(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if name == "" {
		name = path.Base(filePath)
	}
	key := c.objectKey(name)
	if err := c.presignedPut(ctx, key, "", f, st.Size(), nil); err != nil {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return "", err
		}
		cl, err := c.client()
		if err != nil {
			return "", err
		}
		if _, err := cl.PutObject(ctx, c.Bucket, key, f, st.Size(), minio.PutObjectOptions{ContentType: "application/octet-stream"}); err != nil {
			return "", err
		}
	}
	cl, err := c.client()
	if err != nil {
		return "", err
	}
	u, err := cl.PresignedGetObject(ctx, c.Bucket, key, ttl, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}
