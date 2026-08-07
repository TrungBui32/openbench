package storage

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Backend stores files in an S3 bucket (storage.type: s3). Credentials are
// resolved through the standard AWS credential chain at construction time.
type S3Backend struct {
	client *s3.Client
	bucket string
}

func NewS3(ctx context.Context, bucket string) (*S3Backend, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}
	return &S3Backend{
		client: s3.NewFromConfig(cfg),
		bucket: bucket,
	}, nil
}

func (b *S3Backend) Upload(ctx context.Context, localPath, dest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", localPath, err)
	}
	defer f.Close()

	_, err = b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(dest),
		Body:   f,
	})
	if err != nil {
		return fmt.Errorf("uploading to s3://%s/%s: %w", b.bucket, dest, err)
	}
	return nil
}

func (b *S3Backend) Write(ctx context.Context, dest string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(dest),
		Body:   bytes.NewReader(data),
	})
	if err != nil {
		return fmt.Errorf("writing to s3://%s/%s: %w", b.bucket, dest, err)
	}
	return nil
}

func (b *S3Backend) Read(ctx context.Context, dest string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(dest),
	})
	if err != nil {
		return nil, fmt.Errorf("reading s3://%s/%s: %w", b.bucket, dest, err)
	}
	defer out.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(out.Body); err != nil {
		return nil, fmt.Errorf("reading s3://%s/%s: %w", b.bucket, dest, err)
	}
	return buf.Bytes(), nil
}
