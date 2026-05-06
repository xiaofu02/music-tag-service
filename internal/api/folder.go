package api

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"music-tag-service/internal/tag"
)

type FolderItem struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	IsDir      bool      `json:"is_dir"`
	Size       int64     `json:"size"`
	ModTime    time.Time `json:"mod_time"`
	IsAudio    bool      `json:"is_audio"`
	HasLyrics  bool      `json:"has_lyrics"`
	Extension  string    `json:"extension,omitempty"`
}

type FolderListResponse struct {
	Path     string       `json:"path"`
	Parent   string       `json:"parent,omitempty"`
	Items    []FolderItem `json:"items"`
	Count    int          `json:"count"`
	AudioCount int        `json:"audio_count"`
}

func (s *Server) handleFolderList(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		dirPath = s.config.MusicDir
	}

	dirPath = s.sanitizePath(dirPath)

	info, err := os.Stat(dirPath)
	if err != nil {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("path error: %v", err))
		return
	}
	if !info.IsDir() {
		s.sendError(w, http.StatusBadRequest, "path is not a directory")
		return
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("read directory: %v", err))
		return
	}

	sortField := r.URL.Query().Get("sort")
	sortOrder := r.URL.Query().Get("order")

	var items []FolderItem
	audioCount := 0
	lyricsMap := buildLyricsMap(entries)

	for _, entry := range entries {
		name := entry.Name()
		if strings.ToLower(name) == "desktop.ini" {
			continue
		}
		fullPath := filepath.Join(dirPath, name)

		fi, err := entry.Info()
		if err != nil {
			continue
		}

		isAudio := tag.IsAudioFile(name)
		ext := strings.ToLower(filepath.Ext(name))
		hasLyrics := false

		if isAudio {
			audioCount++
			baseName := strings.TrimSuffix(name, ext)
			if _, ok := lyricsMap[baseName]; ok {
				hasLyrics = true
			}
		}

		items = append(items, FolderItem{
			Name:      name,
			Path:      fullPath,
			IsDir:     entry.IsDir(),
			Size:      fi.Size(),
			ModTime:   fi.ModTime(),
			IsAudio:   isAudio,
			HasLyrics: hasLyrics,
			Extension: ext,
		})
	}

	items = sortFolderItems(items, sortField, sortOrder)

	parent := ""
	if dirPath != s.config.MusicDir {
		parent = filepath.Dir(dirPath)
	}

	resp := FolderListResponse{
		Path:       dirPath,
		Parent:     parent,
		Items:      items,
		Count:      len(items),
		AudioCount: audioCount,
	}

	s.sendOKWithElapsed(w, resp, start)
}

func (s *Server) sanitizePath(p string) string {
	p = filepath.Clean(p)
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.config.MusicDir, p)
	}
	return p
}

func buildLyricsMap(entries []fs.DirEntry) map[string]bool {
	m := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".lrc" || ext == ".txt" {
			base := strings.TrimSuffix(name, ext)
			m[base] = true
		}
	}
	return m
}

func sortFolderItems(items []FolderItem, field, order string) []FolderItem {
	dirs := make([]FolderItem, 0)
	files := make([]FolderItem, 0)
	for _, item := range items {
		if item.IsDir {
			dirs = append(dirs, item)
		} else {
			files = append(files, item)
		}
	}

	less := func(a, b FolderItem) bool {
		switch field {
		case "size":
			return a.Size < b.Size
		case "time", "mod_time":
			return a.ModTime.Before(b.ModTime)
		case "name", "":
			fallthrough
		default:
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
	}

	sortFunc := func(slice []FolderItem) {
		sort.Slice(slice, func(i, j int) bool {
			cmp := less(slice[i], slice[j])
			if order == "desc" {
				return !cmp
			}
			return cmp
		})
	}

	sortFunc(dirs)
	sortFunc(files)

	result := make([]FolderItem, 0, len(dirs)+len(files))
	result = append(result, dirs...)
	result = append(result, files...)
	return result
}
