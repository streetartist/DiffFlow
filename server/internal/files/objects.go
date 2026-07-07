package files

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var ErrTooLarge = errors.New("file exceeds sync limit")

type Store struct {
	root string
}

func NewStore(root string) (*Store, error) {
	if root == "" {
		return nil, errors.New("files root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o755); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

func (s *Store) Save(r io.Reader, maxBytes int64) (string, int64, error) {
	tmp, err := os.CreateTemp(filepath.Join(s.root, "tmp"), "upload-*")
	if err != nil {
		return "", 0, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	hasher := sha256.New()
	limited := &limitReader{r: r, remaining: maxBytes + 1}
	written, err := io.Copy(io.MultiWriter(tmp, hasher), limited)
	if closeErr := tmp.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		return "", 0, err
	}
	if written > maxBytes {
		return "", written, ErrTooLarge
	}

	sha := hex.EncodeToString(hasher.Sum(nil))
	finalPath, err := s.objectPath(sha)
	if err != nil {
		return "", 0, err
	}
	if _, err := os.Stat(finalPath); err == nil {
		return sha, written, nil
	}
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return "", 0, err
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return "", 0, err
	}
	return sha, written, nil
}

func (s *Store) Open(sha string) (*os.File, error) {
	path, err := s.objectPath(sha)
	if err != nil {
		return nil, err
	}
	return os.Open(path)
}

func (s *Store) objectPath(sha string) (string, error) {
	if len(sha) != 64 || strings.ContainsAny(sha, `/\`) {
		return "", fmt.Errorf("invalid sha256: %s", sha)
	}
	return filepath.Join(s.root, sha[:2], sha), nil
}

type limitReader struct {
	r         io.Reader
	remaining int64
}

func (r *limitReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > r.remaining {
		p = p[:int(r.remaining)]
	}
	n, err := r.r.Read(p)
	r.remaining -= int64(n)
	return n, err
}
