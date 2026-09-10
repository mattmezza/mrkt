package artifact

import (
	"context"
	"errors"
	"fmt"
	"io"
)

var (
	ErrNotFound = errors.New("artifact not found")
	ErrInvalid  = errors.New("invalid artifact")
)

const DefaultMaxObjectSize int64 = 32 << 20

type Info struct {
	Size        int64
	ContentType string
}

type Store interface {
	Put(ctx context.Context, project, hash, contentType string, size int64, body io.Reader) error
	Get(ctx context.Context, project, hash string) (io.ReadCloser, error)
	Stat(ctx context.Context, project, hash string) (Info, error)
}

func ValidateRef(project, hash string) error {
	if project == "" || len(project) > 128 {
		return fmt.Errorf("%w: invalid project", ErrInvalid)
	}
	for _, r := range project {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return fmt.Errorf("%w: invalid project", ErrInvalid)
		}
	}
	if len(hash) != 64 {
		return fmt.Errorf("%w: hash must be lowercase sha256", ErrInvalid)
	}
	for _, r := range hash {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return fmt.Errorf("%w: hash must be lowercase sha256", ErrInvalid)
		}
	}
	return nil
}
