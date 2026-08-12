package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config points at an S3-compatible bucket (AWS S3, Backblaze B2, Wasabi,
// MinIO, …). Enabled only when endpoint+bucket+keys are all set.
type S3Config struct {
	Endpoint  string // host[:port], no scheme, e.g. s3.amazonaws.com
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string
	Prefix    string // key prefix, e.g. "parkrr/"
	UseSSL    bool
}

func (c S3Config) Enabled() bool {
	return c.Endpoint != "" && c.Bucket != "" && c.AccessKey != "" && c.SecretKey != ""
}

func (c S3Config) client() (*minio.Client, error) {
	if !c.Enabled() {
		return nil, errors.New("S3 is not configured")
	}
	return minio.New(c.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""),
		Secure: c.UseSSL,
		Region: c.Region,
	})
}

func (c S3Config) objectKey(name string) string {
	p := c.Prefix
	if p != "" && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p + name
}

// S3Object is a backup object listed in the bucket.
type S3Object struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// UploadS3 stores an encrypted backup in the bucket; keep>0 prunes to the newest N.
func UploadS3(ctx context.Context, c S3Config, name string, data []byte, keep int) error {
	cl, err := c.client()
	if err != nil {
		return err
	}
	if _, err := cl.PutObject(ctx, c.Bucket, c.objectKey(name), bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"}); err != nil {
		return err
	}
	if keep > 0 {
		pruneS3(ctx, cl, c, keep)
	}
	return nil
}

// Memory guards for S3 (finding SH-06): the bucket is trusted no more than any
// other external input, so a rogue writer or compromised endpoint must not be
// able to exhaust process memory via a huge listing or an oversized object.
const (
	// maxS3ListObjects bounds how many objects a listing/prune accumulates. Real
	// backup buckets hold at most a handful; anything past this is treated as an
	// error rather than read into memory.
	maxS3ListObjects = 10000
	// maxS3DownloadBytes caps a single restored object. Backups are produced and
	// restored fully in memory, so this is a sanity ceiling against an oversized
	// object planted in the bucket (checked via StatObject and enforced on read).
	maxS3DownloadBytes = 2 << 30 // 2 GiB
)

// ListS3 returns the backup objects in the bucket, newest first.
func ListS3(ctx context.Context, c S3Config) ([]S3Object, error) {
	cl, err := c.client()
	if err != nil {
		return nil, err
	}
	return listWith(ctx, cl, c)
}

func listWith(ctx context.Context, cl *minio.Client, c S3Config) ([]S3Object, error) {
	// Cancel the listing on early return so minio-go's producer goroutine doesn't
	// block forever trying to send into a channel we stopped draining.
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var objs []S3Object
	for o := range cl.ListObjects(lctx, c.Bucket, minio.ListObjectsOptions{Prefix: c.objectKey("parkrr-"), Recursive: true}) {
		if o.Err != nil {
			return nil, o.Err
		}
		if len(objs) >= maxS3ListObjects {
			return nil, fmt.Errorf("backup: bucket holds more than %d objects; aborting list to bound memory", maxS3ListObjects)
		}
		objs = append(objs, S3Object{Name: path.Base(o.Key), Size: o.Size, Modified: o.LastModified})
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].Name > objs[j].Name }) // timestamped -> newest first
	return objs, nil
}

// TestS3 checks that the configured bucket is reachable — the backup panel's
// "Verbindung testen". A read-only BucketExists probe: it neither writes nor lists
// objects, so it's a safe, fast connectivity/credentials check.
func TestS3(ctx context.Context, c S3Config) error {
	if !c.Enabled() {
		return errors.New("S3 ist nicht konfiguriert")
	}
	cl, err := c.client()
	if err != nil {
		return err
	}
	ok, err := cl.BucketExists(ctx, c.Bucket)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("bucket %q nicht gefunden", c.Bucket)
	}
	return nil
}

// DownloadS3 fetches one backup object (used for restore-from-S3), bounded to
// maxS3DownloadBytes so an oversized object cannot exhaust memory.
func DownloadS3(ctx context.Context, c S3Config, name string) ([]byte, error) {
	cl, err := c.client()
	if err != nil {
		return nil, err
	}
	key := c.objectKey(name)
	// A cheap HEAD first: reject an over-large object before any body is read.
	st, err := cl.StatObject(ctx, c.Bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return nil, err
	}
	if st.Size > maxS3DownloadBytes {
		return nil, fmt.Errorf("backup: S3 object %q is %d bytes, over the %d-byte limit", name, st.Size, maxS3DownloadBytes)
	}
	obj, err := cl.GetObject(ctx, c.Bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	// Enforce the cap on the actual stream too (guards a size that grows between
	// STAT and GET, or a lying Content-Length): read one byte past the limit and
	// fail if it is reached.
	data, err := io.ReadAll(io.LimitReader(obj, maxS3DownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxS3DownloadBytes {
		return nil, fmt.Errorf("backup: S3 object %q exceeds the %d-byte limit", name, maxS3DownloadBytes)
	}
	return data, nil
}

func pruneS3(ctx context.Context, cl *minio.Client, c S3Config, keep int) {
	objs, err := listWith(ctx, cl, c)
	if err != nil {
		slog.Warn("backup: S3 prune list failed", "bucket", c.Bucket, "err", err)
		return
	}
	if len(objs) <= keep {
		return
	}
	for _, old := range objs[keep:] {
		if err := cl.RemoveObject(ctx, c.Bucket, c.objectKey(old.Name), minio.RemoveObjectOptions{}); err != nil {
			slog.Warn("backup: S3 prune remove failed", "bucket", c.Bucket, "object", old.Name, "err", err)
		}
	}
}
