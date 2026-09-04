// Package blob keeps raw attachment bytes verbatim on disk. It is the first
// thing ingest touches: if a Save fails, nothing else about the dump happens.
// Paths are returned relative to the root so the whole data directory can be
// copied or moved and still resolve.
package blob

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Saved struct {
	Rel    string
	SHA256 string
	Size   int64
}

type Store struct{ root string }

func New(root string) *Store { return &Store{root: root} }

func (s *Store) Root() string { return s.root }

func (s *Store) Abs(rel string) string { return filepath.Join(s.root, rel) }

func (s *Store) itemDir(itemID string) string {
	return filepath.Join("attachments", sanitize(itemID))
}

func sanitize(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.Trim(name, ". ")
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == 0 {
			return '_'
		}
		return r
	}, name)
	if name == "" {
		return "blob"
	}
	return name
}

// Save writes r under the item's directory and returns its relative path and
// hash. The name is made unique per call so identical names never collide.
func (s *Store) Save(itemID, name string, r io.Reader) (Saved, error) {
	relDir := s.itemDir(itemID)
	dir := s.Abs(relDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Saved{}, fmt.Errorf("blob: mkdir: %w", err)
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return Saved{}, fmt.Errorf("blob: create: %w", err)
	}
	tmp := f.Name()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), r)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return Saved{}, fmt.Errorf("blob: write: %w", err)
	}
	final := fmt.Sprintf("%d-%s", time.Now().UnixNano(), sanitize(name))
	if err := os.Rename(tmp, filepath.Join(dir, final)); err != nil {
		os.Remove(tmp)
		return Saved{}, fmt.Errorf("blob: rename: %w", err)
	}
	if err := syncDir(dir); err != nil {
		return Saved{}, fmt.Errorf("blob: sync dir: %w", err)
	}
	return Saved{Rel: filepath.Join(relDir, final), SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}, nil
}

// syncDir makes the rename itself durable; fsyncing the file alone does not
// survive power loss before the directory entry is flushed.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (s *Store) Open(rel string) (*os.File, error) {
	if !s.inside(rel) {
		return nil, errors.New("blob: path outside root")
	}
	return os.Open(s.Abs(rel))
}

func (s *Store) inside(rel string) bool {
	clean := filepath.Clean(rel)
	return !filepath.IsAbs(clean) && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func (s *Store) Remove(rel string) error {
	if !s.inside(rel) {
		return errors.New("blob: path outside root")
	}
	if err := os.Remove(s.Abs(rel)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) RemoveItem(itemID string) error {
	return os.RemoveAll(s.Abs(s.itemDir(itemID)))
}
