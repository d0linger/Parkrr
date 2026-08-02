package backup

import (
	"bytes"
	"context"
	"errors"
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

// ListS3 returns the backup objects in the bucket, newest first.
func ListS3(ctx context.Context, c S3Config) ([]S3Object, error) {
	cl, err := c.client()
	if err != nil {
		return nil, err
	}
	return listWith(ctx, cl, c)
}

func listWith(ctx context.Context, cl *minio.Client, c S3Config) ([]S3Object, error) {
	var objs []S3Object
	for o := range cl.ListObjects(ctx, c.Bucket, minio.ListObjectsOptions{Prefix: c.objectKey("parkrr-"), Recursive: true}) {
		if o.Err != nil {
			return nil, o.Err
		}
		objs = append(objs, S3Object{Name: path.Base(o.Key), Size: o.Size, Modified: o.LastModified})
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].Name > objs[j].Name }) // timestamped -> newest first
	return objs, nil
}

// DownloadS3 fetches one backup object (used for restore-from-S3, no size limit).
func DownloadS3(ctx context.Context, c S3Config, name string) ([]byte, error) {
	cl, err := c.client()
	if err != nil {
		return nil, err
	}
	obj, err := cl.GetObject(ctx, c.Bucket, c.objectKey(name), minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	return io.ReadAll(obj)
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
