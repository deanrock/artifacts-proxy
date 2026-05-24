package proxy

import (
	"artifacts-proxy/pkg/cache"
	"artifacts-proxy/pkg/config"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type s3Store struct {
	client *s3.Client
	cfg    *config.ConfigS3
}

func newS3Store(cfg *config.ConfigS3) (*s3Store, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	var s3Opts []func(*s3.Options)
	if cfg.Endpoint != "" {
		endpoint := cfg.Endpoint
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = true
		})
	}

	return &s3Store{client: s3.NewFromConfig(awsCfg, s3Opts...), cfg: cfg}, nil
}

func (s *s3Store) key(filename string) string {
	if s.cfg.Prefix == "" {
		return filename
	}
	return strings.TrimRight(s.cfg.Prefix, "/") + "/" + filename
}

// fetch downloads hash and hash.metadata from S3 into cacheDir.
func (s *s3Store) fetch(ctx context.Context, hash, cacheDir string) error {
	tmpDir := filepath.Join(cacheDir, "tmp")

	metaPath := filepath.Join(cacheDir, hash+".metadata")
	if err := s.download(ctx, s.key(hash+".metadata"), tmpDir, metaPath); err != nil {
		return err
	}

	cachePath := filepath.Join(cacheDir, hash)
	if err := s.download(ctx, s.key(hash), tmpDir, cachePath); err != nil {
		os.Remove(metaPath)
		return err
	}

	return nil
}

func (s *s3Store) download(ctx context.Context, key, tmpDir, dest string) error {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return err
	}
	defer out.Body.Close()
	return atomicWriteReader(tmpDir, dest, out.Body)
}

// upload pushes hash and hash.metadata from cacheDir to S3. Errors are logged only.
func (s *s3Store) upload(ctx context.Context, hash, cacheDir string) {
	for _, suffix := range []string{"", ".metadata"} {
		path := filepath.Join(cacheDir, hash+suffix)
		key := s.key(hash + suffix)
		if err := s.uploadFile(ctx, key, path); err != nil {
			log.Printf("[s3] upload %s: %v", key, err)
		}
	}
}

func (s *s3Store) uploadFile(ctx context.Context, key, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(key),
		Body:   f,
	})
	return err
}

// getMetadata fetches and parses the metadata file for hash from S3 without writing locally.
func (s *s3Store) getMetadata(ctx context.Context, hash string) (*cache.Metadata, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.cfg.Bucket),
		Key:    aws.String(s.key(hash + ".metadata")),
	})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, err
	}
	var meta cache.Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func isS3NotFound(err error) bool {
	var nsk *s3types.NoSuchKey
	return errors.As(err, &nsk)
}
