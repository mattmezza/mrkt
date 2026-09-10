package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type S3Config struct {
	Region, Bucket, Prefix, Endpoint string
	PathStyle                        bool
	MaxObjectSize                    int64
}
type s3API interface {
	PutObject(context.Context, *s3.PutObjectInput, ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	GetObject(context.Context, *s3.GetObjectInput, ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadObject(context.Context, *s3.HeadObjectInput, ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}
type S3 struct {
	cfg     S3Config
	client  s3API
	uploads chan struct{}
}

func NewS3(ctx context.Context, cfg S3Config) (*S3, error) {
	if cfg.Region == "" || cfg.Bucket == "" {
		return nil, fmt.Errorf("region and bucket are required")
	}
	if cfg.MaxObjectSize <= 0 {
		cfg.MaxObjectSize = DefaultMaxObjectSize
	}
	ac, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}
	client := s3.NewFromConfig(ac, func(o *s3.Options) {
		o.UsePathStyle = cfg.PathStyle
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})
	return &S3{cfg: cfg, client: client, uploads: make(chan struct{}, 4)}, nil
}
func newS3ForTest(cfg S3Config, c s3API) *S3 {
	if cfg.MaxObjectSize <= 0 {
		cfg.MaxObjectSize = DefaultMaxObjectSize
	}
	return &S3{cfg: cfg, client: c, uploads: make(chan struct{}, 4)}
}
func (s *S3) key(project, hash string) string {
	return strings.Trim(s.cfg.Prefix, "/") + func() string {
		if strings.Trim(s.cfg.Prefix, "/") == "" {
			return ""
		}
		return "/"
	}() + "artifacts/" + project + "/objects/" + hash
}
func (s *S3) Put(ctx context.Context, project, hash, contentType string, size int64, body io.Reader) error {
	if err := ValidateRef(project, hash); err != nil {
		return err
	}
	if size < 0 || size > s.cfg.MaxObjectSize {
		return fmt.Errorf("%w: invalid size", ErrInvalid)
	}
	select {
	case s.uploads <- struct{}{}:
		defer func() { <-s.uploads }()
	case <-ctx.Done():
		return ctx.Err()
	}
	tmp, err := os.CreateTemp("", "mrkt-artifact-*")
	if err != nil {
		return fmt.Errorf("create artifact spool: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(body, size+1))
	if err != nil {
		return fmt.Errorf("spool artifact: %w", err)
	}
	if n != size {
		return fmt.Errorf("%w: declared size mismatch", ErrInvalid)
	}
	if hex.EncodeToString(h.Sum(nil)) != hash {
		return fmt.Errorf("%w: digest mismatch", ErrInvalid)
	}
	if _, err = tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &s.cfg.Bucket, Key: aws.String(s.key(project, hash)), Body: tmp, ContentLength: aws.Int64(size), ContentType: aws.String(contentType), IfNoneMatch: aws.String("*"), Metadata: map[string]string{"sha256": hash}})
	if err != nil {
		var ae smithy.APIError
		if !(errors.As(err, &ae) && (ae.ErrorCode() == "PreconditionFailed" || ae.ErrorCode() == "ConditionalRequestConflict")) {
			return fmt.Errorf("put artifact: %w", err)
		}
	}
	info, err := s.Stat(ctx, project, hash)
	if err != nil {
		return fmt.Errorf("verify artifact: %w", err)
	}
	if info.Size != size || info.ContentType != contentType {
		return fmt.Errorf("verify artifact metadata mismatch")
	}
	return nil
}
func (s *S3) Get(ctx context.Context, project, hash string) (io.ReadCloser, error) {
	if err := ValidateRef(project, hash); err != nil {
		return nil, err
	}
	o, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.cfg.Bucket, Key: aws.String(s.key(project, hash))})
	if err != nil {
		var n *s3types.NoSuchKey
		if errors.As(err, &n) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get artifact: %w", err)
	}
	return o.Body, nil
}
func (s *S3) Stat(ctx context.Context, project, hash string) (Info, error) {
	if err := ValidateRef(project, hash); err != nil {
		return Info{}, err
	}
	o, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.cfg.Bucket, Key: aws.String(s.key(project, hash))})
	if err != nil {
		return Info{}, fmt.Errorf("stat artifact: %w", err)
	}
	return Info{aws.ToInt64(o.ContentLength), aws.ToString(o.ContentType)}, nil
}
