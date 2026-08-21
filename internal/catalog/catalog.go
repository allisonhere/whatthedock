package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	dirName       = "compose-catalog"
	indexFileName = "index.json"
	templatesDir  = "templates"
)

type Entry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

type indexFile struct {
	Entries []Entry `json:"entries"`
}

func DirForSettings(settingsPath string) string {
	if strings.TrimSpace(settingsPath) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(settingsPath), dirName)
}

func Load(dir string) ([]Entry, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, indexFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var index indexFile
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	sortEntries(index.Entries)
	return index.Entries, nil
}

func Read(dir, id string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("compose catalog is unavailable without a settings path")
	}
	data, err := os.ReadFile(templatePath(dir, id))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func Save(dir, name, content string) (Entry, error) {
	if strings.TrimSpace(dir) == "" {
		return Entry{}, errors.New("compose catalog is unavailable without a settings path")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Entry{}, errors.New("catalog entry name is required")
	}
	if strings.TrimSpace(content) == "" {
		return Entry{}, errors.New("catalog entry content is empty")
	}
	entries, err := Load(dir)
	if err != nil {
		return Entry{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	entry := Entry{ID: uniqueID(entries, name), Name: name, CreatedAt: now, UpdatedAt: now}
	entries = append(entries, entry)
	if err := writeTemplate(dir, entry.ID, content); err != nil {
		return Entry{}, err
	}
	if err := writeIndex(dir, entries); err != nil {
		_ = os.Remove(templatePath(dir, entry.ID))
		return Entry{}, err
	}
	return entry, nil
}

func Rename(dir, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("catalog entry name is required")
	}
	entries, err := Load(dir)
	if err != nil {
		return err
	}
	for i := range entries {
		if entries[i].ID == id {
			entries[i].Name = name
			entries[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			return writeIndex(dir, entries)
		}
	}
	return fmt.Errorf("catalog entry %q not found", id)
}

func Delete(dir, id string) error {
	entries, err := Load(dir)
	if err != nil {
		return err
	}
	out := entries[:0]
	found := false
	for _, entry := range entries {
		if entry.ID == id {
			found = true
			continue
		}
		out = append(out, entry)
	}
	if !found {
		return fmt.Errorf("catalog entry %q not found", id)
	}
	if err := os.Remove(templatePath(dir, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeIndex(dir, out)
}

func writeTemplate(dir, id, content string) error {
	if err := os.MkdirAll(filepath.Join(dir, templatesDir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(templatePath(dir, id), []byte(content), 0o600)
}

func writeIndex(dir string, entries []Entry) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	sortEntries(entries)
	data, err := json.MarshalIndent(indexFile{Entries: entries}, "", "\t")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(dir, indexFileName), data, 0o600)
}

func templatePath(dir, id string) string {
	return filepath.Join(dir, templatesDir, id+".yml")
}

func uniqueID(entries []Entry, name string) string {
	base := slug(name)
	if base == "" {
		base = "compose"
	}
	used := map[string]bool{}
	for _, entry := range entries {
		used[entry.ID] = true
	}
	if !used[base] {
		return base
	}
	for i := 2; ; i++ {
		id := fmt.Sprintf("%s-%d", base, i)
		if !used[id] {
			return id
		}
	}
}

func slug(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case !lastDash && b.Len() > 0:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
}
