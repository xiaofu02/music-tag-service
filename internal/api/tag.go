package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"music-tag-service/internal/tag"
)

func (s *Server) handleTagRead(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		s.sendError(w, http.StatusBadRequest, "path parameter is required")
		return
	}

	filePath = s.sanitizePath(filePath)

	info, err := os.Stat(filePath)
	if err != nil {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("file error: %v", err))
		return
	}
	if info.IsDir() {
		s.sendError(w, http.StatusBadRequest, "path is a directory, not a file")
		return
	}

	if !tag.IsAudioFile(filePath) {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("not an audio file: %s", filepath.Ext(filePath)))
		return
	}

	includeCover := r.URL.Query().Get("cover") != "false"
	var tagInfo *tag.TagInfo
	if includeCover {
		tagInfo, err = tag.ReadTag(filePath)
	} else {
		tagInfo, err = tag.ReadTagNoCover(filePath)
	}
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("read tag: %v", err))
		return
	}

	s.sendOKWithElapsed(w, tagInfo, start)
}

func (s *Server) handleTagWrite(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req tag.TagWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}

	if req.FilePath == "" {
		s.sendError(w, http.StatusBadRequest, "file_path is required")
		return
	}

	req.FilePath = s.sanitizePath(req.FilePath)

	if _, err := os.Stat(req.FilePath); err != nil {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("file error: %v", err))
		return
	}

	result := tag.WriteTag(req, s.config.FFmpegPath)
	if result.Success {
		s.sendOKWithElapsed(w, result, start)
	} else {
		s.sendJSON(w, http.StatusInternalServerError, Response{OK: false, Error: result.Message, Data: result})
	}
}

type BatchWriteRequest struct {
	Items []tag.TagWriteRequest `json:"items"`
}

func (s *Server) handleTagBatchWrite(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req BatchWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}

	if len(req.Items) == 0 {
		s.sendError(w, http.StatusBadRequest, "items is empty")
		return
	}

	var results []tag.TagWriteResult
	successCount := 0
	failCount := 0

	for i := range req.Items {
		req.Items[i].FilePath = s.sanitizePath(req.Items[i].FilePath)
		result := tag.WriteTag(req.Items[i], s.config.FFmpegPath)
		results = append(results, result)
		if result.Success {
			successCount++
		} else {
			failCount++
		}
	}

	s.sendOKWithElapsed(w, map[string]interface{}{
		"results":       results,
		"total":         len(req.Items),
		"success_count": successCount,
		"fail_count":    failCount,
	}, start)
}

func (s *Server) handleCoverArt(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		s.sendError(w, http.StatusBadRequest, "path parameter is required")
		return
	}

	filePath = s.sanitizePath(filePath)

	if _, err := os.Stat(filePath); err != nil {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("file error: %v", err))
		return
	}

	data, mime, err := tag.ExtractCover(filePath)
	if err != nil {
		s.sendError(w, http.StatusNotFound, fmt.Sprintf("cover art: %v", err))
		return
	}

	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Write(data)
}

type AutoTagRequest struct {
	Paths       []string `json:"paths"`
	Providers   []string `json:"providers"`
	Mode        string   `json:"mode"`
	Concurrency int      `json:"concurrency"`
	Overwrite   bool     `json:"overwrite"`
	SaveCover   bool     `json:"save_cover"`
	SaveLyrics  bool     `json:"save_lyrics"`
}

type AutoTagResult struct {
	FileName string       `json:"file_name"`
	FilePath string       `json:"file_path"`
	Success  bool         `json:"success"`
	Match    *ScrapedSong `json:"match,omitempty"`
	Message  string       `json:"message,omitempty"`
}

const (
	maxAutoTagWorkers = 4
)

func (s *Server) handleAutoTag(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req AutoTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}

	if len(req.Paths) == 0 {
		s.sendError(w, http.StatusBadRequest, "paths is empty")
		return
	}
	if len(req.Providers) == 0 {
		req.Providers = []string{"netease", "qmusic"}
	}
	if req.Mode == "" {
		req.Mode = "simple"
	}
	if req.Concurrency <= 0 {
		req.Concurrency = 4
	}
	if req.Concurrency > 16 {
		req.Concurrency = 16
	}

	var audioFiles []string
	for _, p := range req.Paths {
		p = s.sanitizePath(p)
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if info.IsDir() {
			s.collectAudioFiles(p, &audioFiles)
		} else if tag.IsAudioFile(p) {
			audioFiles = append(audioFiles, p)
		}
	}

	if len(audioFiles) == 0 {
		s.sendError(w, http.StatusBadRequest, "no audio files found")
		return
	}

	workCh := make(chan string, len(audioFiles))
	resultCh := make(chan AutoTagResult, len(audioFiles))

	for i := 0; i < req.Concurrency; i++ {
		go func() {
			for filePath := range workCh {
				result := s.autoTagSingle(filePath, req)
				resultCh <- result
			}
		}()
	}

	go func() {
		for _, filePath := range audioFiles {
			workCh <- filePath
		}
		close(workCh)
	}()

	var results []AutoTagResult
	successCount := 0
	failCount := 0

	for i := 0; i < len(audioFiles); i++ {
		result := <-resultCh
		results = append(results, result)
		if result.Success {
			successCount++
		} else {
			failCount++
		}
	}

	s.sendOKWithElapsed(w, map[string]interface{}{
		"results":       results,
		"total":         len(results),
		"success_count": successCount,
		"fail_count":    failCount,
	}, start)
}

func (s *Server) collectAudioFiles(dir string, files *[]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		fullPath := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			s.collectAudioFiles(fullPath, files)
		} else if tag.IsAudioFile(fullPath) {
			*files = append(*files, fullPath)
		}
	}
}

func (s *Server) autoTagSingle(filePath string, req AutoTagRequest) AutoTagResult {
	fileName := filepath.Base(filePath)

	if !tag.IsAudioFile(filePath) {
		return AutoTagResult{FileName: fileName, FilePath: filePath, Success: false, Message: "not an audio file"}
	}

	tagInfo, err := tag.ReadTagNoCover(filePath)
	if err != nil {
		return AutoTagResult{FileName: fileName, FilePath: filePath, Success: false, Message: fmt.Sprintf("read tag: %v", err)}
	}

	searchTitle := tagInfo.Title
	if searchTitle == "" {
		searchTitle = strings.TrimSuffix(tagInfo.FileName, filepath.Ext(tagInfo.FileName))
	}

	type searchResult struct {
		songs    []ScrapedSong
		provider string
		err      error
	}

	searchCh := make(chan searchResult, len(req.Providers))
	for _, provider := range req.Providers {
		go func(prov string) {
			songs, err := Search(prov, searchTitle)
			searchCh <- searchResult{songs: songs, provider: prov, err: err}
		}(provider)
	}

	var allSongs []ScrapedSong
	for range req.Providers {
		sr := <-searchCh
		if sr.err != nil || len(sr.songs) == 0 {
			continue
		}
		allSongs = append(allSongs, sr.songs...)
	}
	close(searchCh)

	if len(allSongs) == 0 {
		return AutoTagResult{
			FileName: fileName, FilePath: filePath, Success: false,
			Message: fmt.Sprintf("no match found from providers: %v", req.Providers),
		}
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
		yearInt, _ = strconv.Atoi(bestMatch.Year)
	}

	writeReq := tag.TagWriteRequest{
		FilePath:       filePath,
		Title:          bestMatch.Name,
		Artist:         bestMatch.Artist,
		Album:          bestMatch.Album,
		Year:           yearInt,
		SaveCoverFile:  req.SaveCover,
		SaveLyricsFile: req.SaveLyrics,
	}

	if bestMatch.AlbumImg != "" {
		writeReq.CoverURL = bestMatch.AlbumImg
	}

	if bestMatch.ID != "" {
		lyric, err := FetchLyric(bestMatch.Provider, bestMatch.ID)
		if err == nil && lyric != "" {
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

	result := tag.WriteTag(writeReq, s.config.FFmpegPath)
	return AutoTagResult{
		FileName: fileName,
		FilePath: filePath,
		Success:  result.Success,
		Match:    bestMatch,
		Message:  result.Message,
	}
}

type OrganizeRequest struct {
	Paths     []string `json:"paths"`
	RootPath  string   `json:"root_path"`
	FirstDir  string   `json:"first_dir"`
	SecondDir string   `json:"second_dir"`
	DryRun    bool     `json:"dry_run"`
}

type OrganizePlan struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type BatchRenameRequest struct {
	Paths     []string       `json:"paths"`
	Template string          `json:"template"`
	Provider string          `json:"provider"`
	Overwrite bool           `json:"overwrite"`
}

type BatchRenameResult struct {
	FileName string `json:"file_name"`
	FilePath string `json:"file_path"`
	OldName  string `json:"old_name"`
	NewName  string `json:"new_name"`
	Success  bool   `json:"success"`
	Message  string `json:"message,omitempty"`
}

func (s *Server) handleBatchRename(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req BatchRenameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}

	if len(req.Paths) == 0 {
		s.sendError(w, http.StatusBadRequest, "paths is empty")
		return
	}

	if req.Template == "" {
		req.Template = "{artist} - {title}"
	}

	var results []BatchRenameResult
	successCount := 0
	failCount := 0

	for _, filePath := range req.Paths {
		filePath = s.sanitizePath(filePath)
		oldPath := filePath
		result := s.batchRenameSingle(filePath, req)
		results = append(results, result)
		if result.Success {
			successCount++
			s.updateProcessedFilePath(oldPath, result.FilePath)
		} else {
			failCount++
		}
	}

	s.sendOKWithElapsed(w, map[string]interface{}{
		"results":       results,
		"total":         len(results),
		"success_count": successCount,
		"fail_count":    failCount,
	}, start)
}

func (s *Server) batchRenameSingle(filePath string, req BatchRenameRequest) BatchRenameResult {
	fileName := filepath.Base(filePath)
	ext := filepath.Ext(fileName)

	if !tag.IsAudioFile(filePath) {
		return BatchRenameResult{
			FileName: fileName, FilePath: filePath,
			Success: false, Message: "not an audio file",
		}
	}

	tagInfo, err := tag.ReadTagNoCover(filePath)
	if err != nil {
		return BatchRenameResult{
			FileName: fileName, FilePath: filePath,
			Success: false, Message: fmt.Sprintf("read tag: %v", err),
		}
	}

	newName := buildFileName(req.Template, tagInfo)
	newName = sanitizeFileName(newName)
	newName = cleanRepeatedChars(newName)
	newName += ext

	if newName == fileName || newName == ext {
		return BatchRenameResult{
			FileName: fileName, FilePath: filePath,
			OldName: fileName, NewName: fileName,
			Success: false, Message: "no change needed",
		}
	}

	dir := filepath.Dir(filePath)
	newPath := filepath.Join(dir, newName)

	if _, err := os.Stat(newPath); err == nil && filePath != newPath {
		timestamp := fmt.Sprintf("_%d", time.Now().UnixNano()%1000000)
		nameWithoutExt := strings.TrimSuffix(newName, ext)
		newName = nameWithoutExt + timestamp + ext
		newPath = filepath.Join(dir, newName)
	}

	if err := os.Rename(filePath, newPath); err != nil {
		return BatchRenameResult{
			FileName: fileName, FilePath: filePath,
			Success: false, Message: fmt.Sprintf("rename: %v", err),
		}
	}

	return BatchRenameResult{
		FileName: newName, FilePath: newPath,
		OldName: fileName, NewName: newName,
		Success: true,
	}
}

func buildFileName(template string, info *tag.TagInfo) string {
	title := info.Title
	if title == "" {
		title = strings.TrimSuffix(info.FileName, filepath.Ext(info.FileName))
	}
	artist := info.Artist
	album := info.Album
	albumArtist := info.AlbumArtist
	year := ""
	if info.Year > 0 {
		year = fmt.Sprintf("%d", info.Year)
	}
	track := ""
	if info.TrackNumber > 0 {
		track = fmt.Sprintf("%02d", info.TrackNumber)
	}

	result := template
	result = strings.ReplaceAll(result, "{title}", title)
	result = strings.ReplaceAll(result, "{artist}", artist)
	result = strings.ReplaceAll(result, "{album}", album)
	result = strings.ReplaceAll(result, "{album_artist}", albumArtist)
	result = strings.ReplaceAll(result, "{year}", year)
	result = strings.ReplaceAll(result, "{track}", track)

	result = strings.TrimSpace(result)
	result = strings.Trim(result, " -_.")

	return result
}

func sanitizeFileName(name string) string {
	invalid := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	for _, c := range invalid {
		name = strings.ReplaceAll(name, c, "_")
	}
	return strings.TrimSpace(name)
}

func cleanRepeatedChars(name string) string {
	name = strings.ReplaceAll(name, ",,", ",")
	name = strings.ReplaceAll(name, ",,", ",")
	name = strings.ReplaceAll(name, "  ", " ")
	name = strings.ReplaceAll(name, " - - ", " - ")
	name = strings.ReplaceAll(name, "- -", "-")
	name = strings.ReplaceAll(name, "--", "-")
	name = strings.Trim(name, " -_")
	return name
}

func (s *Server) handleOrganize(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req OrganizeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}

	if len(req.Paths) == 0 {
		s.sendError(w, http.StatusBadRequest, "paths is empty")
		return
	}
	if req.FirstDir == "" {
		req.FirstDir = "artist"
	}
	if req.RootPath == "" {
		req.RootPath = s.config.MusicDir
	}

	var plans []OrganizePlan
	for _, p := range req.Paths {
		p = s.sanitizePath(p)
		if !tag.IsAudioFile(p) {
			continue
		}

		tagInfo, err := tag.ReadTagNoCover(p)
		if err != nil {
			continue
		}

		firstValue := getTagField(tagInfo, req.FirstDir)
		secondValue := ""
		if req.SecondDir != "" {
			secondValue = getTagField(tagInfo, req.SecondDir)
		}

		var targetPath string
		if secondValue != "" {
			targetPath = filepath.Join(req.RootPath, sanitizeDirName(firstValue), sanitizeDirName(secondValue), filepath.Base(p))
		} else {
			targetPath = filepath.Join(req.RootPath, sanitizeDirName(firstValue), filepath.Base(p))
		}

		if p != targetPath {
			plans = append(plans, OrganizePlan{Source: p, Target: targetPath})
		}
	}

	if !req.DryRun {
		for i := range plans {
			targetDir := filepath.Dir(plans[i].Target)
			os.MkdirAll(targetDir, 0755)
			sourcePath := plans[i].Source
			if err := os.Rename(plans[i].Source, plans[i].Target); err != nil {
				plans[i].Target = fmt.Sprintf("ERROR: %v", err)
			} else {
				s.updateProcessedFilePath(sourcePath, plans[i].Target)
			}
		}
	}

	s.sendOKWithElapsed(w, map[string]interface{}{
		"plans":     plans,
		"total":     len(plans),
		"executed":  !req.DryRun,
	}, start)
}

func getTagField(info *tag.TagInfo, field string) string {
	switch strings.ToLower(field) {
	case "artist":
		return info.Artist
	case "album":
		return info.Album
	case "album_artist", "albumartist":
		return info.AlbumArtist
	case "genre":
		return info.Genre
	case "year":
		if info.Year > 0 {
			return fmt.Sprintf("%d", info.Year)
		}
		return "Unknown"
	default:
		return info.Artist
	}
}

func sanitizeDirName(name string) string {
	if name == "" {
		return "Unknown"
	}
	return strings.Map(func(r rune) rune {
		if r == '\\' || r == '/' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
			return '_'
		}
		return r
	}, name)
}

type ApplyRequest struct {
	FilePath      string `json:"file_path"`
	Title         string `json:"title,omitempty"`
	Artist        string `json:"artist,omitempty"`
	Album         string `json:"album,omitempty"`
	AlbumArtist   string `json:"album_artist,omitempty"`
	Genre         string `json:"genre,omitempty"`
	Year          int    `json:"year,omitempty"`
	SongID        string `json:"song_id,omitempty"`
	Provider      string `json:"provider,omitempty"`
	CoverURL      string `json:"cover_url,omitempty"`
	FetchLyric    bool   `json:"fetch_lyric"`
	SaveCoverFile bool   `json:"save_cover_file"`
	SaveLyricsFile bool  `json:"save_lyrics_file"`
}

func (s *Server) handleApply(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req ApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("decode request: %v", err))
		return
	}

	if req.FilePath == "" {
		s.sendError(w, http.StatusBadRequest, "file_path is required")
		return
	}

	req.FilePath = s.sanitizePath(req.FilePath)

	if _, err := os.Stat(req.FilePath); err != nil {
		s.sendError(w, http.StatusBadRequest, fmt.Sprintf("file error: %v", err))
		return
	}

	writeReq := tag.TagWriteRequest{
		FilePath:       req.FilePath,
		Title:          req.Title,
		Artist:         req.Artist,
		Album:          req.Album,
		AlbumArtist:    req.AlbumArtist,
		Genre:          req.Genre,
		Year:           req.Year,
		CoverURL:       req.CoverURL,
		SaveCoverFile:  req.SaveCoverFile,
		SaveLyricsFile: req.SaveLyricsFile,
	}

	if req.FetchLyric && req.SongID != "" && req.Provider != "" {
		lyric, err := FetchLyric(req.Provider, req.SongID)
		if err == nil && lyric != "" {
			writeReq.Lyrics = lyric
		}
	}

	result := tag.WriteTag(writeReq, s.config.FFmpegPath)

	s.sendOKWithElapsed(w, map[string]interface{}{
		"file_path":    req.FilePath,
		"success":      result.Success,
		"message":      result.Message,
		"lyric_fetched": writeReq.Lyrics != "",
		"cover_applied": req.CoverURL != "",
	}, start)
}
