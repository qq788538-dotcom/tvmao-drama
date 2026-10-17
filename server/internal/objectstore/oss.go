package objectstore

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

func init() { providers["oss"] = newOSSClient }

// ossClient 是阿里云 OSS 的 Client 实现。
type ossClient struct {
	cli    *oss.Client
	bucket string
}

func newOSSClient(c Config) (Client, error) {
	cfg := oss.LoadDefaultConfig().
		WithCredentialsProvider(credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey)).
		WithRegion(c.Region).
		WithEndpoint(c.Endpoint)
	return &ossClient{cli: oss.NewClient(cfg), bucket: c.Bucket}, nil
}

func (o *ossClient) Exists(ctx context.Context, key string) (bool, error) {
	ok, err := o.cli.IsObjectExist(ctx, o.bucket, key)
	if err != nil {
		return false, fmt.Errorf("oss head object: %w", err)
	}
	return ok, nil
}

func (o *ossClient) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := o.cli.PutObject(ctx, &oss.PutObjectRequest{
		Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(key),
		ContentType: oss.Ptr(contentType), ContentLength: oss.Ptr(size),
		Body: r,
	})
	if err != nil {
		return fmt.Errorf("oss put object: %w", err)
	}
	return nil
}

func (o *ossClient) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	res, err := o.cli.Presign(ctx, &oss.GetObjectRequest{Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(key)},
		oss.PresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("oss presign: %w", err)
	}
	return res.URL, nil
}

func (o *ossClient) Delete(ctx context.Context, key string) error {
	if _, err := o.cli.DeleteObject(ctx, &oss.DeleteObjectRequest{Bucket: oss.Ptr(o.bucket), Key: oss.Ptr(key)}); err != nil {
		return fmt.Errorf("oss delete object: %w", err)
	}
	return nil
}
