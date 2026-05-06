package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"music-tag-service/internal/config"
	"music-tag-service/internal/tag"
)

type AutoImporter struct {
	cfg       config.AutoImportConfig
	watcher   *config.Watcher
	stopCh    chan struct{}
	resultCh  chan AutoImportResult
	mu        sync.RWMutex
	running   bool
	ffmpegPath string
	recentFiles   map[string]time.Time
	processedFiles map[string]time.Time
}

type ProcessedFilesStore struct {
	Files map[string]int64 `json:"files"`
}

func (ai *AutoImporter) loadProcessedFiles() {
	ai.mu.Lock()
	defer ai.mu.Unlock()

	filePath := filepath.Join(config.GetConfigDir(), "processed_files.json")
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.Printf("auto-import: no existing processed files record")
		ai.processedFiles = make(map[string]time.Time)
		return
	}

	var store ProcessedFilesStore
	if err := json.Unmarshal(data, &store); err != nil {
		log.Printf("auto-import: failed to parse processed files: %v", err)
		ai.processedFiles = make(map[string]time.Time)
		return
	}

	ai.processedFiles = make(map[string]time.Time)
	for path, timestamp := range store.Files {
		ai.processedFiles[path] = time.Unix(timestamp, 0)
	}
	log.Printf("auto-import: loaded %d processed files", len(ai.processedFiles))
}

func (ai *AutoImporter) saveProcessedFiles() {
	ai.mu.Lock()
	defer ai.mu.Unlock()

	store := ProcessedFilesStore{
		Files: make(map[string]int64),
	}
	for path, t := range ai.processedFiles {
		store.Files[path] = t.Unix()
	}

	filePath := filepath.Join(config.GetConfigDir(), "processed_files.json")
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		log.Printf("auto-import: failed to marshal processed files: %v", err)
		return
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		log.Printf("auto-import: failed to save processed files: %v", err)
		return
	}
	log.Printf("auto-import: saved %d processed files", len(store.Files))
}

func (ai *AutoImporter) markFileProcessed(filePath string) {
	ai.mu.Lock()
	ai.processedFiles[filePath] = time.Now()
	ai.mu.Unlock()
	go ai.saveProcessedFiles()
}

func (ai *AutoImporter) isFileProcessed(filePath string) bool {
	ai.mu.RLock()
	defer ai.mu.RUnlock()
	_, exists := ai.processedFiles[filePath]
	return exists
}

func (ai *AutoImporter) renameProcessedFile(oldPath, newPath string) {
	ai.mu.Lock()
	defer ai.mu.Unlock()

	if _, exists := ai.processedFiles[oldPath]; exists {
		timestamp := ai.processedFiles[oldPath]
		delete(ai.processedFiles, oldPath)
		ai.processedFiles[newPath] = timestamp
		log.Printf("auto-import: updated processed file path from '%s' to '%s'", oldPath, newPath)
		go ai.saveProcessedFiles()
	}
}

func (s *Server) updateProcessedFilePath(oldPath, newPath string) {
	s.watcherMu.RLock()
	defer s.watcherMu.RUnlock()

	if s.autoImport != nil {
		s.autoImport.renameProcessedFile(oldPath, newPath)
	}
}

type AutoImportConfig struct {
	WatchPath   string
	Concurrency int
	AutoTag    bool
	Providers  []string
	Mode      string
	Overwrite  bool
}

type AutoImportResult struct {
	Path     string
	Success  bool
	Message  string
	Match    *ScrapedSong
}

type ImportRequest struct {
	Paths      []string `json:"paths"`
	Providers  []string `json:"providers"`
	Mode       string   `json:"mode"`
	Concurrency int     `json:"concurrency"`
	Overwrite  bool     `json:"overwrite"`
	AutoTag   bool     `json:"auto_tag"`
}

type ImportResult struct {
	Path     string       `json:"path"`
	Success  bool         `json:"success"`
	Message  string       `json:"message,omitempty"`
	Files    int         `json:"files"`
	Matched  int         `json:"matched"`
	Errors   int         `json:"errors"`
}

func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	cfg := s.cfgManager.Get()
	s.sendOK(w, cfg)
}

func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	var cfg config.Config
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("decode: %v", err))
		return
	}

	if err := s.cfgManager.Update(cfg); err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("save config: %v", err))
		return
	}

	s.sendOK(w, map[string]string{"message": "config saved"})
}

func (s *Server) handleStartAutoImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WatchPath   string   `json:"watch_path"`
		Concurrency  int      `json:"concurrency"`
		AutoTag     bool     `json:"auto_tag"`
		Providers   []string `json:"providers"`
		Mode        string   `json:"mode"`
		Overwrite   bool     `json:"overwrite"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("decode: %v", err))
		return
	}

	if req.WatchPath == "" {
		s.sendError(w, http.StatusBadRequest, "watch_path is required")
		return
	}

	info, err := os.Stat(req.WatchPath)
	if err != nil || !info.IsDir() {
		s.sendError(w, http.StatusBadRequest, "invalid watch_path")
		return
	}

	if req.Concurrency <= 0 {
		req.Concurrency = 4
	}
	if req.Concurrency > 16 {
		req.Concurrency = 16
	}
	if req.Providers == nil || len(req.Providers) == 0 {
		req.Providers = []string{"netease", "qmusic"}
	}
	if req.Mode == "" {
		req.Mode = "simple"
	}

	autoImport := &AutoImporter{
		cfg: config.AutoImportConfig{
			Enabled:      true,
			WatchPath:   req.WatchPath,
			Concurrency:  req.Concurrency,
			AutoTag:     req.AutoTag,
			Providers:   req.Providers,
			Mode:        req.Mode,
			Overwrite:   req.Overwrite,
		},
		stopCh:        make(chan struct{}),
		resultCh:      make(chan AutoImportResult, 100),
		recentFiles:   make(map[string]time.Time),
		processedFiles: make(map[string]time.Time),
		ffmpegPath:    s.config.FFmpegPath,
	}

	if err := s.cfgManager.UpdateAutoImport(autoImport.cfg); err != nil {
		log.Printf("failed to save auto-import config: %v", err)
	}

	watcher, err := config.NewWatcher(req.WatchPath)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("create watcher: %v", err))
		return
	}

	s.watcherMu.Lock()
	s.autoImport = autoImport
	s.autoImport.watcher = watcher
	s.watcherMu.Unlock()

	autoImport.loadProcessedFiles()
	autoImport.Start()

	s.sendOK(w, map[string]interface{}{
		"message":    "auto-import started",
		"watch_path": req.WatchPath,
		"concurrency": req.Concurrency,
	})
}

func (s *Server) StartAutoImport(cfg AutoImportConfig) error {
	autoImport := &AutoImporter{
		cfg: config.AutoImportConfig{
			Enabled:      true,
			WatchPath:   cfg.WatchPath,
			Concurrency:  cfg.Concurrency,
			AutoTag:     cfg.AutoTag,
			Providers:   cfg.Providers,
			Mode:        cfg.Mode,
			Overwrite:   cfg.Overwrite,
		},
		stopCh:        make(chan struct{}),
		resultCh:      make(chan AutoImportResult, 100),
		recentFiles:   make(map[string]time.Time),
		processedFiles: make(map[string]time.Time),
		ffmpegPath:   s.config.FFmpegPath,
	}

	watcher, err := config.NewWatcher(cfg.WatchPath)
	if err != nil {
		return fmt.Errorf("create watcher: %v", err)
	}

	autoImport.loadProcessedFiles()

	s.watcherMu.Lock()
	s.autoImport = autoImport
	s.autoImport.watcher = watcher
	s.watcherMu.Unlock()

	autoImport.Start()
	return nil
}

func (s *Server) handleStopAutoImport(w http.ResponseWriter, r *http.Request) {
	s.watcherMu.Lock()
	defer s.watcherMu.Unlock()

	if s.autoImport == nil || !s.autoImport.running {
		s.sendOK(w, map[string]string{"message": "auto-import not running"})
		return
	}

	s.autoImport.Stop()
	s.autoImport = nil

	sendOK := s.cfgManager.Get()
	sendOK.AutoImport.Enabled = false
	s.cfgManager.Update(sendOK)

	s.sendOK(w, map[string]string{"message": "auto-import stopped"})
}

func (ai *AutoImporter) Start() {
	ai.mu.Lock()
	ai.running = true
	ai.stopCh = make(chan struct{})
	ai.mu.Unlock()

	go ai.run()
	go ai.processResults()
}

func (ai *AutoImporter) Stop() {
	ai.mu.Lock()
	if !ai.running {
		ai.mu.Unlock()
		return
	}
	ai.running = false
	ai.mu.Unlock()

	close(ai.stopCh)
	if ai.watcher != nil {
		ai.watcher.Stop()
	}
}

func (ai *AutoImporter) run() {
	if ai.watcher == nil {
		log.Printf("auto-import: watcher is nil, cannot start")
		return
	}

	log.Printf("auto-import: starting with watch path: %s", ai.cfg.WatchPath)
	ai.watcher.Start()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ai.stopCh:
			log.Printf("auto-import: stop signal received")
			return
		case events, ok := <-ai.watcher.Events():
			if !ok {
				log.Printf("auto-import: watcher events channel closed")
				return
			}
			log.Printf("auto-import: received %d events", len(events))
			go ai.processEvents(events)
		case <-ticker.C:
			ai.cleanupRecentFiles()
		}
	}
}

func (ai *AutoImporter) cleanupRecentFiles() {
	ai.mu.Lock()
	defer ai.mu.Unlock()
	now := time.Now()
	for path, t := range ai.recentFiles {
		if now.Sub(t) > 10*time.Minute {
			delete(ai.recentFiles, path)
		}
	}
	log.Printf("auto-import: cleaned up recent files, %d entries remaining", len(ai.recentFiles))
}

func (ai *AutoImporter) processEvents(events []config.WatchEvent) {
	log.Printf("auto-import: processing %d events", len(events))
	for _, event := range events {
		ai.mu.Lock()
		running := ai.running
		ai.mu.Unlock()
		if !running {
			log.Printf("auto-import: not running, skipping event for %s", event.Path)
			continue
		}
		log.Printf("auto-import: event op=%v(%d) path=%s", event.Op, event.Op, event.Path)
		if event.Op == config.FsnotifyCreate {
			log.Printf("auto-import: processing create event for: %s", event.Path)
			ai.processFile(event.Path)
		} else {
			log.Printf("auto-import: ignoring event with op=%v", event.Op)
		}
	}
	log.Printf("auto-import: finished processing events")
}

func (ai *AutoImporter) processFile(filePath string) {
	log.Printf("auto-import: === processing file: %s ===", filePath)

	if !tag.IsAudioFile(filePath) {
		log.Printf("auto-import: not an audio file: %s", filePath)
		return
	}

	if ai.isFileProcessed(filePath) {
		log.Printf("auto-import: skipping previously processed file: %s", filePath)
		return
	}

	ai.mu.Lock()
	if lastTime, ok := ai.recentFiles[filePath]; ok {
		if time.Since(lastTime) < 30*time.Second {
			ai.mu.Unlock()
			log.Printf("auto-import: skipping recently processed file: %s", filePath)
			return
		}
	}
	ai.recentFiles[filePath] = time.Now()
	ai.mu.Unlock()

	result := AutoImportResult{
		Path: filePath,
	}

	log.Printf("auto-import: reading tag from: %s", filePath)
	tagInfo, tagErr := tag.ReadTagNoCover(filePath)
	if tagErr != nil {
		result.Success = false
		result.Message = tagErr.Error()
		log.Printf("auto-import: failed to read tag for %s: %v", filePath, tagErr)
		ai.resultCh <- result
		return
	}

	searchTitle := tagInfo.Title
	if searchTitle == "" {
		searchTitle = strings.TrimSuffix(tagInfo.FileName, filepath.Ext(tagInfo.FileName))
	}

	log.Printf("auto-import: searching for '%s' (artist: %s)", searchTitle, tagInfo.Artist)

	type searchResult struct {
		songs    []ScrapedSong
		provider string
		err     error
	}

	searchCh := make(chan searchResult, len(ai.cfg.Providers))
	log.Printf("auto-import: searching providers: %v", ai.cfg.Providers)
	for _, prov := range ai.cfg.Providers {
		go func(p string) {
			songs, err := Search(p, searchTitle)
			searchCh <- searchResult{songs: songs, provider: p, err: err}
		}(prov)
	}

	var allSongs []ScrapedSong
	log.Printf("auto-import: waiting for search results...")
	for range ai.cfg.Providers {
		sr := <-searchCh
		if sr.err != nil || len(sr.songs) == 0 {
			log.Printf("auto-import: %s: no results (%v)", sr.provider, sr.err)
			continue
		}
		log.Printf("auto-import: %s: found %d songs", sr.provider, len(sr.songs))
		allSongs = append(allSongs, sr.songs...)
	}

	if len(allSongs) == 0 {
		result.Message = "no match found"
		result.Success = false
		log.Printf("auto-import: no songs found for '%s'", searchTitle)
		ai.resultCh <- result
		return
	}

	log.Printf("auto-import: found %d songs for '%s'", len(allSongs), searchTitle)

	bestMatch := matchSong(searchTitle, tagInfo.Artist, tagInfo.Album, allSongs, ai.cfg.Mode)
	if bestMatch == nil && ai.cfg.Mode == "hard" {
		bestMatch = matchSong(searchTitle, tagInfo.Artist, tagInfo.Album, allSongs, "simple")
	}
	if bestMatch == nil {
		bestMatch = &allSongs[0]
	}

	log.Printf("auto-import: best match: %s - %s", bestMatch.Artist, bestMatch.Name)
	result.Match = bestMatch

	if !ai.cfg.AutoTag {
		result.Success = true
		result.Message = "found match, auto-tag disabled"
		log.Printf("auto-import: auto-tag disabled, skipping write")
		ai.resultCh <- result
		return
	}

	ai.writeTag(filePath, bestMatch, result)
}

func (ai *AutoImporter) writeTag(filePath string, bestMatch *ScrapedSong, result AutoImportResult) {
	log.Printf("auto-import: writing tag to: %s", filePath)
	yearInt := 0
	if bestMatch.Year != "" {
		fmt.Sscanf(bestMatch.Year, "%d", &yearInt)
	}

	writeReq := tag.TagWriteRequest{
		FilePath:  filePath,
		Title:     bestMatch.Name,
		Artist:    bestMatch.Artist,
		Album:     bestMatch.Album,
		Year:      yearInt,
	}

	if bestMatch.AlbumImg != "" {
		writeReq.CoverURL = bestMatch.AlbumImg
	}

	if bestMatch.ID != "" {
		log.Printf("auto-import: fetching lyric for song ID: %s", bestMatch.ID)
		lyric, _ := FetchLyric(bestMatch.Provider, bestMatch.ID)
		if lyric != "" {
			writeReq.Lyrics = lyric
		}
	}

	log.Printf("auto-import: calling WriteTag...")
	wr := tag.WriteTag(writeReq, ai.ffmpegPath)
	log.Printf("auto-import: WriteTag result: success=%v, message=%s", wr.Success, wr.Message)
	if wr.Success {
		result.Success = true
		result.Message = "tag written"
		ai.markFileProcessed(filePath)
	} else {
		result.Success = false
		result.Message = wr.Message
	}

	ai.resultCh <- result
}

func (ai *AutoImporter) processResults() {
	for {
		select {
		case <-ai.stopCh:
			return
		case result, ok := <-ai.resultCh:
			if !ok {
				return
			}
			if result.Success {
				log.Printf("auto-import: success - %s", result.Path)
			} else {
				log.Printf("auto-import: failed - %s: %s", result.Path, result.Message)
			}
		}
	}
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	var req ImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("decode: %v", err))
		return
	}

	if len(req.Paths) == 0 {
		s.sendError(w, http.StatusBadRequest, "paths is required")
		return
	}

	if req.Concurrency <= 0 {
		req.Concurrency = 4
	}
	if req.Concurrency > 16 {
		req.Concurrency = 16
	}
	if req.Providers == nil || len(req.Providers) == 0 {
		req.Providers = []string{"netease", "qmusic"}
	}
	if req.Mode == "" {
		req.Mode = "simple"
	}

	var allFiles []string
	for _, p := range req.Paths {
		p = s.sanitizePath(p)
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.IsDir() {
			s.collectAudioFiles(p, &allFiles)
		} else if tag.IsAudioFile(p) {
			allFiles = append(allFiles, p)
		}
	}

	if len(allFiles) == 0 {
		s.sendError(w, http.StatusBadRequest, "no audio files found")
		return
	}

	result := ImportResult{
		Path:    req.Paths[0],
		Files:   len(allFiles),
	}

	workCh := make(chan string, len(allFiles))
	resultCh := make(chan AutoTagResult, len(allFiles))

	for i := 0; i < req.Concurrency; i++ {
		go func() {
			for filePath := range workCh {
				r := s.importSingle(filePath, req)
				resultCh <- r
			}
		}()
	}

	go func() {
		for _, f := range allFiles {
			workCh <- f
		}
		close(workCh)
	}()

	for i := 0; i < len(allFiles); i++ {
		r := <-resultCh
		if r.Success {
			result.Matched++
		} else {
			result.Errors++
		}
	}

	s.sendOKWithElapsed(w, result, start)
}

func (s *Server) importSingle(filePath string, req ImportRequest) AutoTagResult {
	tagInfo, err := tag.ReadTagNoCover(filePath)
	if err != nil {
		return AutoTagResult{FileName: filepath.Base(filePath), FilePath: filePath, Success: false, Message: err.Error()}
	}

	searchTitle := tagInfo.Title
	if searchTitle == "" {
		searchTitle = strings.TrimSuffix(tagInfo.FileName, filepath.Ext(tagInfo.FileName))
	}

	type searchResult struct {
		songs    []ScrapedSong
		provider string
		err     error
	}

	searchCh := make(chan searchResult, len(req.Providers))
	for _, prov := range req.Providers {
		go func(p string) {
			songs, err := Search(p, searchTitle)
			searchCh <- searchResult{songs: songs, provider: p, err: err}
		}(prov)
	}

	var allSongs []ScrapedSong
	for range req.Providers {
		sr := <-searchCh
		if sr.err != nil || len(sr.songs) == 0 {
			continue
		}
		allSongs = append(allSongs, sr.songs...)
	}

	if len(allSongs) == 0 {
		return AutoTagResult{FileName: filepath.Base(filePath), FilePath: filePath, Success: false, Message: "no match found"}
	}

	bestMatch := matchSong(searchTitle, tagInfo.Artist, tagInfo.Album, allSongs, req.Mode)
	if bestMatch == nil && req.Mode == "hard" {
		bestMatch = matchSong(searchTitle, tagInfo.Artist, tagInfo.Album, allSongs, "simple")
	}
	if bestMatch == nil {
		bestMatch = &allSongs[0]
	}

	yearInt := 0
	if bestMatch.Year != "" {
		fmt.Sscanf(bestMatch.Year, "%d", &yearInt)
	}

	writeReq := tag.TagWriteRequest{
		FilePath:  filePath,
		Title:     bestMatch.Name,
		Artist:    bestMatch.Artist,
		Album:     bestMatch.Album,
		Year:      yearInt,
	}

	if bestMatch.AlbumImg != "" {
		writeReq.CoverURL = bestMatch.AlbumImg
	}

	if bestMatch.ID != "" {
		lyric, _ := FetchLyric(bestMatch.Provider, bestMatch.ID)
		if lyric != "" {
			writeReq.Lyrics = lyric
		}
	}

	if !req.Overwrite {
		if tagInfo.Title != "" {
			writeReq.Title = ""
		}
		if tagInfo.Artist != "" {
			writeReq.Artist = ""
		}
		if tagInfo.Album != "" {
			writeReq.Album = ""
		}
	}

	wr := tag.WriteTag(writeReq, s.config.FFmpegPath)
	return AutoTagResult{
		FileName: filepath.Base(filePath),
		FilePath: filePath,
		Success:  wr.Success,
		Match:    bestMatch,
		Message:  wr.Message,
	}
}
