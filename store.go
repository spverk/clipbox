package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrTooLarge     = errors.New("too large")
	ErrLastText     = errors.New("last text")
	ErrTooManyTexts = errors.New("too many texts")
)

const (
	// MaxTextBytes 单个文字窗口的最大内容（2 MiB）。
	MaxTextBytes = 2 << 20
	// MaxTexts 最多允许的窗口数量。
	MaxTexts = 30
)

// FileMeta 文件槽的元数据。
type FileMeta struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	Updated time.Time `json:"updated"`
}

// TextMeta 文字窗口的元数据。
type TextMeta struct {
	ID      int       `json:"id"`
	Title   string    `json:"title"`
	Updated time.Time `json:"updated"`
}

type textIndex struct {
	NextID int        `json:"next_id"`
	Items  []TextMeta `json:"items"`
}

// Store 用普通文件保存全部数据，不依赖数据库。
type Store struct {
	mu       sync.Mutex
	uploadMu sync.Mutex
	dir      string
	fileMeta FileMeta
	texts    textIndex
}

func NewStore(dir string) (*Store, error) {
	s := &Store{dir: dir}
	for _, d := range []string{s.fileDir(), s.textDir()} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return nil, err
		}
	}
	if err := s.loadFile(); err != nil {
		return nil, err
	}
	if err := s.loadTexts(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) fileDir() string      { return filepath.Join(s.dir, "file") }
func (s *Store) textDir() string      { return filepath.Join(s.dir, "texts") }
func (s *Store) filePath() string     { return filepath.Join(s.fileDir(), "current.bin") }
func (s *Store) fileMetaPath() string { return filepath.Join(s.fileDir(), "meta.json") }
func (s *Store) textIndexPath() string {
	return filepath.Join(s.textDir(), "index.json")
}
func (s *Store) textPath(id int) string {
	return filepath.Join(s.textDir(), strconv.Itoa(id)+".txt")
}

func (s *Store) loadFile() error {
	var meta FileMeta
	if b, err := os.ReadFile(s.fileMetaPath()); err == nil {
		_ = json.Unmarshal(b, &meta)
	}
	st, err := os.Stat(s.filePath())
	switch {
	case err == nil:
		meta.Size = st.Size()
		meta.Updated = st.ModTime().UTC()
		if meta.Name == "" {
			meta.Name = "file.bin"
		}
	case errors.Is(err, os.ErrNotExist):
		meta = FileMeta{}
	default:
		return err
	}
	s.fileMeta = meta
	return nil
}

func (s *Store) loadTexts() error {
	var idx textIndex
	if b, err := os.ReadFile(s.textIndexPath()); err == nil {
		_ = json.Unmarshal(b, &idx)
	}

	seen := make(map[int]bool, len(idx.Items))
	items := make([]TextMeta, 0, len(idx.Items))
	for _, it := range idx.Items {
		if it.ID <= 0 || seen[it.ID] {
			continue
		}
		if _, err := os.Stat(s.textPath(it.ID)); err != nil {
			continue
		}
		seen[it.ID] = true
		items = append(items, it)
	}

	if len(items) == 0 {
		ents, err := os.ReadDir(s.textDir())
		if err != nil {
			return err
		}
		for _, e := range ents {
			name := e.Name()
			if !strings.HasSuffix(name, ".txt") {
				continue
			}
			id, err := strconv.Atoi(strings.TrimSuffix(name, ".txt"))
			if err != nil || id <= 0 || seen[id] {
				continue
			}
			seen[id] = true
			meta := TextMeta{ID: id, Title: fmt.Sprintf("窗口 %d", id)}
			if fi, err := e.Info(); err == nil {
				meta.Updated = fi.ModTime().UTC()
			}
			items = append(items, meta)
		}
	}

	if len(items) == 0 {
		for i := 1; i <= 3; i++ {
			if err := os.WriteFile(s.textPath(i), nil, 0o640); err != nil {
				return err
			}
			items = append(items, TextMeta{ID: i, Title: fmt.Sprintf("窗口 %d", i), Updated: time.Now().UTC()})
			seen[i] = true
		}
	}

	sort.Slice(items, func(a, b int) bool { return items[a].ID < items[b].ID })

	next := idx.NextID
	for id := range seen {
		if id+1 > next {
			next = id + 1
		}
	}
	if next <= 0 {
		next = 1
	}

	s.texts = textIndex{NextID: next, Items: items}
	return s.saveTextIndex()
}

func (s *Store) saveTextIndex() error {
	b, err := json.MarshalIndent(s.texts, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(s.textIndexPath(), b, 0o640)
}

func (s *Store) FileInfo() FileMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fileMeta
}

// SaveFile 流式写入临时文件，然后原子替换 current.bin。
func (s *Store) SaveFile(name string, r io.Reader, limit int64) (FileMeta, error) {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()

	tmp, err := os.CreateTemp(s.fileDir(), ".upload-*")
	if err != nil {
		return FileMeta{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	n, err := io.Copy(tmp, r)
	if err != nil {
		tmp.Close()
		return FileMeta{}, err
	}
	if err := tmp.Close(); err != nil {
		return FileMeta{}, err
	}
	if n > limit {
		return FileMeta{}, ErrTooLarge
	}
	if err := os.Chmod(tmpName, 0o640); err != nil {
		return FileMeta{}, err
	}
	if err := os.Rename(tmpName, s.filePath()); err != nil {
		return FileMeta{}, err
	}

	meta := FileMeta{Name: cleanName(name), Size: n, Updated: time.Now().UTC()}
	s.mu.Lock()
	s.fileMeta = meta
	s.mu.Unlock()

	if b, err := json.MarshalIndent(meta, "", "  "); err == nil {
		if err := writeAtomic(s.fileMetaPath(), b, 0o640); err != nil {
			return meta, err
		}
	}
	return meta, nil
}

func (s *Store) OpenFile() (*os.File, FileMeta, error) {
	s.mu.Lock()
	meta := s.fileMeta
	s.mu.Unlock()
	if meta.Updated.IsZero() {
		return nil, FileMeta{}, ErrNotFound
	}
	f, err := os.Open(s.filePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, FileMeta{}, ErrNotFound
		}
		return nil, FileMeta{}, err
	}
	return f, meta, nil
}

func (s *Store) DeleteFile() error {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range []string{s.filePath(), s.fileMetaPath()} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	s.fileMeta = FileMeta{}
	return nil
}

func (s *Store) ListTexts() []TextMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TextMeta, len(s.texts.Items))
	copy(out, s.texts.Items)
	return out
}

func (s *Store) CreateText(title string) (TextMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.texts.Items) >= MaxTexts {
		return TextMeta{}, ErrTooManyTexts
	}
	id := s.texts.NextID
	if id <= 0 {
		id = 1
	}
	meta := TextMeta{
		ID:      id,
		Title:   cleanTitle(title, fmt.Sprintf("窗口 %d", id)),
		Updated: time.Now().UTC(),
	}
	if err := os.WriteFile(s.textPath(id), nil, 0o640); err != nil {
		return TextMeta{}, err
	}
	s.texts.NextID = id + 1
	s.texts.Items = append(s.texts.Items, meta)
	if err := s.saveTextIndex(); err != nil {
		return TextMeta{}, err
	}
	return meta, nil
}

func (s *Store) GetText(id int) (TextMeta, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	meta, ok := s.findText(id)
	if !ok {
		return TextMeta{}, "", ErrNotFound
	}
	b, err := os.ReadFile(s.textPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return TextMeta{}, "", ErrNotFound
		}
		return TextMeta{}, "", err
	}
	return meta, string(b), nil
}

func (s *Store) SaveText(id int, content string) (TextMeta, error) {
	if len(content) > MaxTextBytes {
		return TextMeta{}, ErrTooLarge
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.findText(id); !ok {
		return TextMeta{}, ErrNotFound
	}
	if err := writeAtomic(s.textPath(id), []byte(content), 0o640); err != nil {
		return TextMeta{}, err
	}
	s.touchText(id)
	if err := s.saveTextIndex(); err != nil {
		return TextMeta{}, err
	}
	return s.texts.Items[s.textIndex(id)], nil
}

func (s *Store) RenameText(id int, title string) (TextMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, idx, ok := s.findTextAt(id)
	if !ok {
		return TextMeta{}, ErrNotFound
	}
	s.texts.Items[idx].Title = cleanTitle(title, fmt.Sprintf("窗口 %d", id))
	s.touchText(id)
	if err := s.saveTextIndex(); err != nil {
		return TextMeta{}, err
	}
	return s.texts.Items[idx], nil
}

func (s *Store) DeleteText(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, idx, ok := s.findTextAt(id)
	if !ok {
		return ErrNotFound
	}
	if len(s.texts.Items) <= 1 {
		return ErrLastText
	}
	if err := os.Remove(s.textPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	s.texts.Items = append(s.texts.Items[:idx], s.texts.Items[idx+1:]...)
	return s.saveTextIndex()
}

func (s *Store) findText(id int) (TextMeta, bool) {
	_, idx, ok := s.findTextAt(id)
	if !ok {
		return TextMeta{}, false
	}
	return s.texts.Items[idx], true
}

func (s *Store) findTextAt(id int) (TextMeta, int, bool) {
	idx := s.textIndex(id)
	if idx < 0 {
		return TextMeta{}, 0, false
	}
	return s.texts.Items[idx], idx, true
}

func (s *Store) textIndex(id int) int {
	for i, it := range s.texts.Items {
		if it.ID == id {
			return i
		}
	}
	return -1
}

func (s *Store) touchText(id int) {
	if idx := s.textIndex(id); idx >= 0 {
		s.texts.Items[idx].Updated = time.Now().UTC()
	}
}

func cleanName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if r == 0 || unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(strings.Trim(name, "."))
	if name == "" || name == "/" {
		return "file.bin"
	}
	if rs := []rune(name); len(rs) > 120 {
		name = string(rs[:120])
	}
	return name
}

func cleanTitle(title, fallback string) string {
	title = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || unicode.IsControl(r) {
			return ' '
		}
		return r
	}, title)
	title = strings.Join(strings.Fields(title), " ")
	if rs := []rune(title); len(rs) > 60 {
		title = string(rs[:60])
	}
	if title == "" {
		return fallback
	}
	return title
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
