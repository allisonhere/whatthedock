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
	entriesDir    = "entries"

	NoteHeaderStart = "# WhatTheDock note:"
	NoteHeaderEnd   = "# End WhatTheDock note"

	StatusSaved   = "saved"
	StatusDraft   = "draft"
	StatusApplied = "applied"
)

type Entry struct {
	ID           string      `json:"id"`
	Name         string      `json:"name"`
	CreatedAt    string      `json:"createdAt,omitempty"`
	UpdatedAt    string      `json:"updatedAt,omitempty"`
	LastSeenAt   string      `json:"lastSeenAt,omitempty"`
	Note         string      `json:"note,omitempty"`
	Tags         []string    `json:"tags,omitempty"`
	Status       string      `json:"status,omitempty"`
	Archived     bool        `json:"archived,omitempty"`
	SourceSystem string      `json:"sourceSystem,omitempty"`
	SourcePaths  []string    `json:"sourcePaths,omitempty"`
	PrimaryFile  string      `json:"primaryFile,omitempty"`
	Files        []EntryFile `json:"files,omitempty"`
}

type EntryFile struct {
	Name       string `json:"name"`
	SourcePath string `json:"sourcePath,omitempty"`
	Primary    bool   `json:"primary,omitempty"`
}

type FileContent struct {
	Name       string
	SourcePath string
	Content    string
	Primary    bool
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
	for i := range index.Entries {
		index.Entries[i].Status = normalizeStatus(index.Entries[i].Status)
	}
	sortEntries(index.Entries)
	return index.Entries, nil
}

func Read(dir, id string) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("compose catalog is unavailable without a settings path")
	}
	if entry, ok, err := findEntry(dir, id); err != nil {
		return "", err
	} else if ok && len(entry.Files) > 0 {
		files, err := ReadFiles(dir, id)
		if err != nil {
			return "", err
		}
		for _, file := range files {
			if file.Primary {
				return file.Content, nil
			}
		}
		return files[0].Content, nil
	}
	data, err := os.ReadFile(templatePath(dir, id))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func ReadFiles(dir, id string) ([]FileContent, error) {
	entry, ok, err := findEntry(dir, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("catalog entry %q not found", id)
	}
	if len(entry.Files) == 0 {
		content, err := Read(dir, id)
		if err != nil {
			return nil, err
		}
		name := id + ".yml"
		return []FileContent{{Name: name, Content: content, Primary: true}}, nil
	}
	files := make([]FileContent, 0, len(entry.Files))
	for _, file := range entry.Files {
		data, err := os.ReadFile(entryFilePath(dir, id, file.Name))
		if err != nil {
			return nil, err
		}
		files = append(files, FileContent{Name: file.Name, SourcePath: file.SourcePath, Content: string(data), Primary: file.Primary})
	}
	return files, nil
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
	entry := Entry{ID: uniqueID(entries, name), Name: name, CreatedAt: now, UpdatedAt: now, Status: StatusSaved}
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

func SaveStack(dir, name, note, sourceSystem string, sourcePaths []string, files []FileContent) (Entry, error) {
	if strings.TrimSpace(dir) == "" {
		return Entry{}, errors.New("compose catalog is unavailable without a settings path")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Entry{}, errors.New("catalog entry name is required")
	}
	if len(files) == 0 {
		return Entry{}, errors.New("catalog entry has no compose files")
	}
	entries, err := Load(dir)
	if err != nil {
		return Entry{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	entry := Entry{
		ID:           uniqueID(entries, name),
		Name:         name,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastSeenAt:   now,
		Note:         strings.TrimSpace(note),
		Status:       StatusSaved,
		SourceSystem: strings.TrimSpace(sourceSystem),
		SourcePaths:  compactStrings(sourcePaths),
	}
	seenNames := map[string]bool{}
	primarySet := false
	for i, file := range files {
		file.Name = safeFileName(file.Name, i)
		if seenNames[file.Name] {
			file.Name = uniqueFileName(seenNames, file.Name)
		}
		seenNames[file.Name] = true
		if strings.TrimSpace(file.Content) == "" {
			return Entry{}, errors.New("catalog entry content is empty")
		}
		if !primarySet && (file.Primary || i == 0) {
			file.Primary = true
			entry.PrimaryFile = file.Name
			primarySet = true
		} else {
			file.Primary = false
		}
		if file.Primary {
			file.Content = ApplyNoteHeader(file.Content, entry.Note)
		}
		entry.Files = append(entry.Files, EntryFile{Name: file.Name, SourcePath: file.SourcePath, Primary: file.Primary})
		if err := writeEntryFile(dir, entry.ID, file.Name, file.Content); err != nil {
			_ = os.RemoveAll(entryDirPath(dir, entry.ID))
			return Entry{}, err
		}
	}
	entries = append(entries, entry)
	if err := writeIndex(dir, entries); err != nil {
		_ = os.RemoveAll(entryDirPath(dir, entry.ID))
		return Entry{}, err
	}
	return entry, nil
}

func ReplaceStack(dir, id, note, sourceSystem string, sourcePaths []string, files []FileContent) (Entry, error) {
	if strings.TrimSpace(dir) == "" {
		return Entry{}, errors.New("compose catalog is unavailable without a settings path")
	}
	if len(files) == 0 {
		return Entry{}, errors.New("catalog entry has no compose files")
	}
	entries, err := Load(dir)
	if err != nil {
		return Entry{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range entries {
		if entries[i].ID != id {
			continue
		}
		entry := entries[i]
		if strings.TrimSpace(note) != "" {
			entry.Note = strings.TrimSpace(note)
		}
		entry.UpdatedAt = now
		entry.LastSeenAt = now
		entry.Status = StatusSaved
		entry.SourceSystem = strings.TrimSpace(sourceSystem)
		entry.SourcePaths = compactStrings(sourcePaths)
		entry.Files = nil
		entry.PrimaryFile = ""
		if err := os.RemoveAll(entryDirPath(dir, entry.ID)); err != nil {
			return Entry{}, err
		}
		if err := writeStackFiles(dir, &entry, files); err != nil {
			_ = os.RemoveAll(entryDirPath(dir, entry.ID))
			return Entry{}, err
		}
		entries[i] = entry
		if err := writeIndex(dir, entries); err != nil {
			return Entry{}, err
		}
		return entry, nil
	}
	return Entry{}, fmt.Errorf("catalog entry %q not found", id)
}

func DuplicateAsDraft(dir, id string) (Entry, error) {
	source, ok, err := findEntry(dir, id)
	if err != nil {
		return Entry{}, err
	}
	if !ok {
		return Entry{}, fmt.Errorf("catalog entry %q not found", id)
	}
	files, err := ReadFiles(dir, id)
	if err != nil {
		return Entry{}, err
	}
	entries, err := Load(dir)
	if err != nil {
		return Entry{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	entry := Entry{
		ID:           uniqueID(entries, source.Name+" draft"),
		Name:         source.Name + " draft",
		CreatedAt:    now,
		UpdatedAt:    now,
		LastSeenAt:   source.LastSeenAt,
		Note:         source.Note,
		Tags:         append([]string(nil), source.Tags...),
		Status:       StatusDraft,
		SourceSystem: source.SourceSystem,
		SourcePaths:  append([]string(nil), source.SourcePaths...),
	}
	if err := writeStackFiles(dir, &entry, files); err != nil {
		_ = os.RemoveAll(entryDirPath(dir, entry.ID))
		return Entry{}, err
	}
	entries = append(entries, entry)
	if err := writeIndex(dir, entries); err != nil {
		_ = os.RemoveAll(entryDirPath(dir, entry.ID))
		return Entry{}, err
	}
	return entry, nil
}

func TouchSeen(dir, id string) error {
	return updateEntry(dir, id, func(entry *Entry) error {
		entry.LastSeenAt = time.Now().UTC().Format(time.RFC3339)
		return nil
	})
}

func UpdateNote(dir, id, note string) error {
	return updateEntry(dir, id, func(entry *Entry) error {
		entry.Note = strings.TrimSpace(note)
		entry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if len(entry.Files) == 0 {
			content, err := Read(dir, id)
			if err != nil {
				return err
			}
			return writeTemplate(dir, id, ApplyNoteHeader(content, entry.Note))
		}
		primary := entry.PrimaryFile
		if primary == "" {
			primary = entry.Files[0].Name
		}
		data, err := os.ReadFile(entryFilePath(dir, id, primary))
		if err != nil {
			return err
		}
		return writeEntryFile(dir, id, primary, ApplyNoteHeader(string(data), entry.Note))
	})
}

func SetTags(dir, id string, tags []string) error {
	return updateEntry(dir, id, func(entry *Entry) error {
		entry.Tags = NormalizeTags(tags)
		entry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		return nil
	})
}

func UpdatePrimaryFile(dir, id, content string) error {
	if strings.TrimSpace(content) == "" {
		return errors.New("catalog entry content is empty")
	}
	return updateEntry(dir, id, func(entry *Entry) error {
		entry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		entry.Status = StatusDraft
		content = ApplyNoteHeader(content, entry.Note)
		if len(entry.Files) == 0 {
			return writeTemplate(dir, id, content)
		}
		primary := entry.PrimaryFile
		if primary == "" {
			primary = entry.Files[0].Name
		}
		return writeEntryFile(dir, id, primary, content)
	})
}

func SetArchived(dir, id string, archived bool) error {
	return updateEntry(dir, id, func(entry *Entry) error {
		entry.Archived = archived
		entry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		return nil
	})
}

func SetStatus(dir, id, status string) error {
	status = normalizeStatus(status)
	return updateEntry(dir, id, func(entry *Entry) error {
		entry.Status = status
		entry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		return nil
	})
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
	if err := os.RemoveAll(entryDirPath(dir, id)); err != nil {
		return err
	}
	return writeIndex(dir, out)
}

func NormalizeTags(tags []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(tags))
	for _, value := range tags {
		for _, part := range strings.Split(value, ",") {
			tag := strings.TrimPrefix(strings.TrimSpace(part), "#")
			tag = strings.ToLower(tag)
			if tag == "" || seen[tag] {
				continue
			}
			seen[tag] = true
			out = append(out, tag)
		}
	}
	sort.Strings(out)
	return out
}

func ApplyNoteHeader(content, note string) string {
	content = removeNoteHeader(content)
	note = strings.TrimSpace(note)
	if note == "" {
		return content
	}
	var lines []string
	lines = append(lines, NoteHeaderStart)
	for _, line := range strings.Split(note, "\n") {
		lines = append(lines, "# "+strings.TrimRight(line, "\r"))
	}
	lines = append(lines, NoteHeaderEnd)
	header := strings.Join(lines, "\n") + "\n"
	if content != "" && !strings.HasPrefix(content, "\n") {
		header += "\n"
	}
	return header + content
}

func removeNoteHeader(content string) string {
	lines := strings.SplitAfter(content, "\n")
	start, end := -1, -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == NoteHeaderStart {
			start = i
			continue
		}
		if start >= 0 && trimmed == NoteHeaderEnd {
			end = i
			break
		}
	}
	if start < 0 || end < start {
		return content
	}
	out := append([]string{}, lines[:start]...)
	out = append(out, lines[end+1:]...)
	if len(out) > 0 && strings.TrimSpace(strings.Join(lines[start:end+1], "")) != "" && strings.TrimSpace(out[0]) == "" {
		out = out[1:]
	}
	return strings.Join(out, "")
}

func writeTemplate(dir, id, content string) error {
	if err := os.MkdirAll(filepath.Join(dir, templatesDir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(templatePath(dir, id), []byte(content), 0o600)
}

func writeEntryFile(dir, id, name, content string) error {
	if err := os.MkdirAll(entryDirPath(dir, id), 0o755); err != nil {
		return err
	}
	return os.WriteFile(entryFilePath(dir, id, name), []byte(content), 0o600)
}

func writeStackFiles(dir string, entry *Entry, files []FileContent) error {
	seenNames := map[string]bool{}
	primarySet := false
	for i, file := range files {
		file.Name = safeFileName(file.Name, i)
		if seenNames[file.Name] {
			file.Name = uniqueFileName(seenNames, file.Name)
		}
		seenNames[file.Name] = true
		if strings.TrimSpace(file.Content) == "" {
			return errors.New("catalog entry content is empty")
		}
		if !primarySet && (file.Primary || i == 0) {
			file.Primary = true
			entry.PrimaryFile = file.Name
			primarySet = true
		} else {
			file.Primary = false
		}
		if file.Primary {
			file.Content = ApplyNoteHeader(file.Content, entry.Note)
		}
		entry.Files = append(entry.Files, EntryFile{Name: file.Name, SourcePath: file.SourcePath, Primary: file.Primary})
		if err := writeEntryFile(dir, entry.ID, file.Name, file.Content); err != nil {
			return err
		}
	}
	return nil
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

func entryDirPath(dir, id string) string {
	return filepath.Join(dir, entriesDir, id)
}

func entryFilePath(dir, id, name string) string {
	return filepath.Join(entryDirPath(dir, id), filepath.Base(name))
}

func findEntry(dir, id string) (Entry, bool, error) {
	entries, err := Load(dir)
	if err != nil {
		return Entry{}, false, err
	}
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true, nil
		}
	}
	return Entry{}, false, nil
}

func updateEntry(dir, id string, update func(*Entry) error) error {
	entries, err := Load(dir)
	if err != nil {
		return err
	}
	for i := range entries {
		if entries[i].ID != id {
			continue
		}
		if err := update(&entries[i]); err != nil {
			return err
		}
		return writeIndex(dir, entries)
	}
	return fmt.Errorf("catalog entry %q not found", id)
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

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func safeFileName(name string, index int) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = fmt.Sprintf("compose-%d.yml", index+1)
	}
	if !strings.HasSuffix(strings.ToLower(name), ".yml") && !strings.HasSuffix(strings.ToLower(name), ".yaml") {
		name += ".yml"
	}
	return name
}

func uniqueFileName(used map[string]bool, name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", base, i, ext)
		if !used[candidate] {
			return candidate
		}
	}
}

func normalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case StatusDraft:
		return StatusDraft
	case StatusApplied:
		return StatusApplied
	default:
		return StatusSaved
	}
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
}
