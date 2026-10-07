package objectstore

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos/enum"
)

func init() { providers["tos"] = newTOSClient }

// tosClient 是火山引擎 TOS 的 Client 实现。
type tosClient struct {
	cli    *tos.ClientV2
	bucket string
}

func newTOSClient(c Config) (Client, error) {
	cli, err := tos.NewClientV2(
		c.Endpoint,
		tos.WithRegion(c.Region),
		tos.WithCredentials(tos.NewStaticCredentials(c.AccessKey, c.SecretKey)),
	)
	if err != nil {
		return nil, fmt.Errorf("tos new client: %w", err)
	}
	return &tosClient{cli: cli, bucket: c.Bucket}, nil
}

func (t *tosClient) Exists(ctx context.Context, key string) (bool, error) {
	_, err := t.cli.HeadObjectV2(ctx, &tos.HeadObjectV2Input{Bucket: t.bucket, Key: key})
	if err == nil {
		return true, nil
	}
	if tos.StatusCode(err) == http.StatusNotFound {
		return false, nil
	}
	return false, fmt.Errorf("tos head object: %w", err)
}

func (t *tosClient) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := t.cli.PutObjectV2(ctx, &tos.PutObjectV2Input{
		PutObjectBasicInput: tos.PutObjectBasicInput{
			Bucket: t.bucket, Key: key, ContentType: contentType, ContentLength: size,
		},
		Content: r,
	})
	if err != nil {
		return fmt.Errorf("tos put object: %w", err)
	}
	return nil
}

func (t *tosClient) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	out, err := t.cli.PreSignedURL(&tos.PreSignedURLInput{
		HTTPMethod: enum.HttpMethodGet, Bucket: t.bucket, Key: key, Expires: int64(ttl.Seconds()),
	})
	if err != nil {
		return "", fmt.Errorf("tos presign: %w", err)
	}
	return out.SignedUrl, nil
}

func (t *tosClient) Delete(ctx context.Context, key string) error {
	if _, err := t.cli.DeleteObjectV2(ctx, &tos.DeleteObjectV2Input{Bucket: t.bucket, Key: key}); err != nil {
		return fmt.Errorf("tos delete object: %w", err)
	}
	return nil
}
