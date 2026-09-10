package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
)

type Memory struct {
	MaxObjectSize int64
	mu            sync.RWMutex
	objects       map[string]memoryObject
}
type memoryObject struct {
	data        []byte
	contentType string
}

func NewMemory(max int64) *Memory {
	if max <= 0 {
		max = DefaultMaxObjectSize
	}
	return &Memory{MaxObjectSize: max, objects: make(map[string]memoryObject)}
}
func objectKey(project, hash string) string { return project + "/" + hash }
func (m *Memory) Put(ctx context.Context, project, hash, contentType string, size int64, body io.Reader) error {
	if err := ValidateRef(project, hash); err != nil {
		return err
	}
	if size < 0 || size > m.MaxObjectSize {
		return fmt.Errorf("%w: invalid size", ErrInvalid)
	}
	r := io.LimitReader(body, size+1)
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	if int64(len(b)) != size {
		return fmt.Errorf("%w: declared size mismatch", ErrInvalid)
	}
	s := sha256.Sum256(b)
	if hex.EncodeToString(s[:]) != hash {
		return fmt.Errorf("%w: digest mismatch", ErrInvalid)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := objectKey(project, hash)
	if old, ok := m.objects[k]; ok && (!bytes.Equal(old.data, b) || old.contentType != contentType) {
		return fmt.Errorf("%w: immutable object conflict", ErrInvalid)
	}
	m.objects[k] = memoryObject{append([]byte(nil), b...), contentType}
	return nil
}
func (m *Memory) Get(_ context.Context, project, hash string) (io.ReadCloser, error) {
	if err := ValidateRef(project, hash); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	o, ok := m.objects[objectKey(project, hash)]
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(o.data)), nil
}
func (m *Memory) Stat(_ context.Context, project, hash string) (Info, error) {
	if err := ValidateRef(project, hash); err != nil {
		return Info{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	o, ok := m.objects[objectKey(project, hash)]
	if !ok {
		return Info{}, ErrNotFound
	}
	return Info{int64(len(o.data)), o.contentType}, nil
}
