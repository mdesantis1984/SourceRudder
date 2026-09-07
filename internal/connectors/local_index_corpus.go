package connectors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const (
	localIndexVersion      = 1
	maxLocalIndexBytes     = 4 << 20
	maxLocalIndexDocuments = 10_000
)

type localIndexCorpus struct {
	Version   int                  `json:"version"`
	Documents []localIndexDocument `json:"documents"`
}

type localIndexDocument struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	URL     string   `json:"url,omitempty"`
	Snippet string   `json:"snippet,omitempty"`
	Author  string   `json:"author,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

func loadLocalIndexCorpus(path string) ([]localIndexDocument, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("local index path is empty")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve local index path: %w", err)
	}
	rootPath, name := filepath.Dir(absPath), filepath.Base(absPath)
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open local index directory: %w", err)
	}
	defer root.Close()

	info, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("inspect local index: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("local index must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("local index must be a regular file")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("local index must not be group- or world-writable")
	}
	if info.Size() > maxLocalIndexBytes {
		return nil, fmt.Errorf("local index exceeds %d bytes", maxLocalIndexBytes)
	}

	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open local index: %w", err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened local index: %w", err)
	}
	if !os.SameFile(info, openedInfo) {
		return nil, fmt.Errorf("local index changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxLocalIndexBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read local index: %w", err)
	}
	if len(data) > maxLocalIndexBytes {
		return nil, fmt.Errorf("local index exceeds %d bytes", maxLocalIndexBytes)
	}

	var corpus localIndexCorpus
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&corpus); err != nil {
		return nil, fmt.Errorf("decode local index: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("decode local index: trailing JSON content")
	}
	if corpus.Version != localIndexVersion {
		return nil, fmt.Errorf("unsupported local index version %d", corpus.Version)
	}
	if len(corpus.Documents) > maxLocalIndexDocuments {
		return nil, fmt.Errorf("local index exceeds %d documents", maxLocalIndexDocuments)
	}

	seen := make(map[string]struct{}, len(corpus.Documents))
	for i := range corpus.Documents {
		if err := validateLocalIndexDocument(&corpus.Documents[i], seen); err != nil {
			return nil, fmt.Errorf("local index document %d: %w", i, err)
		}
	}
	return corpus.Documents, nil
}

func validateLocalIndexDocument(doc *localIndexDocument, seen map[string]struct{}) error {
	doc.ID = strings.TrimSpace(doc.ID)
	doc.Title = strings.TrimSpace(doc.Title)
	doc.URL = strings.TrimSpace(doc.URL)
	doc.Snippet = strings.TrimSpace(doc.Snippet)
	doc.Author = strings.TrimSpace(doc.Author)
	if doc.ID == "" || utf8.RuneCountInString(doc.ID) > 128 || !isSafeLocalIndexID(doc.ID) {
		return fmt.Errorf("id must contain 1-128 ASCII letters, digits, dots, underscores, or hyphens")
	}
	if _, ok := seen[doc.ID]; ok {
		return fmt.Errorf("duplicate id %q", doc.ID)
	}
	seen[doc.ID] = struct{}{}
	if doc.Title == "" || utf8.RuneCountInString(doc.Title) > 512 {
		return fmt.Errorf("title must contain 1-512 characters")
	}
	if utf8.RuneCountInString(doc.Snippet) > 4096 || utf8.RuneCountInString(doc.Author) > 256 {
		return fmt.Errorf("snippet or author exceeds its length limit")
	}
	if len(doc.Tags) > 32 {
		return fmt.Errorf("tags exceed the limit of 32")
	}
	for i := range doc.Tags {
		doc.Tags[i] = strings.TrimSpace(doc.Tags[i])
		if doc.Tags[i] == "" || utf8.RuneCountInString(doc.Tags[i]) > 128 {
			return fmt.Errorf("tag %d must contain 1-128 characters", i)
		}
	}

	safeURL, err := safeLocalIndexURL(doc.URL, doc.ID)
	if err != nil {
		return err
	}
	doc.URL = safeURL
	return nil
}

func isSafeLocalIndexID(id string) bool {
	for _, r := range id {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r)) {
			return false
		}
	}
	return true
}

func safeLocalIndexURL(raw, id string) (string, error) {
	if raw == "" {
		return "local-index://document/" + id, nil
	}
	if utf8.RuneCountInString(raw) > 2048 {
		return "", fmt.Errorf("url exceeds 2048 characters")
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("url must be an absolute http, https, or local-index URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("url must not contain credentials, query parameters, or fragments")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "local-index" {
		return "", fmt.Errorf("url scheme %q is not allowed", parsed.Scheme)
	}
	return parsed.String(), nil
}
