package api

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"music-tag-service/internal/config"
	"music-tag-service/pkg/dialog"
)

type Config struct {
	Port         int
	MusicDir     string
	Token        string
	FFmpegPath   string
	FFprobePath  string
	FFmpegAvail  bool
	FFprobeAvail bool
	WebFS        embed.FS
}

type Server struct {
	config        Config
	mux           *http.ServeMux
	cfgManager    *config.Manager
	watcherMu     sync.RWMutex
	autoImport    *AutoImporter
}

type Response struct {
	OK      bool        `json:"ok"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
	Elapsed string      `json:"elapsed,omitempty"`
}

func NewServer(cfg Config, cfgManager *config.Manager) *Server {
	s := &Server{
		config:     cfg,
		mux:        http.NewServeMux(),
		cfgManager: cfgManager,
	}

	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/capabilities", s.handleCapabilities)
	s.mux.HandleFunc("GET /api/v1/folder", s.auth(s.handleFolderList))
	s.mux.HandleFunc("GET /api/v1/tag", s.auth(s.handleTagRead))
	s.mux.HandleFunc("PUT /api/v1/tag", s.auth(s.handleTagWrite))
	s.mux.HandleFunc("POST /api/v1/tag/batch", s.auth(s.handleTagBatchWrite))
	s.mux.HandleFunc("GET /api/v1/cover", s.auth(s.handleCoverArt))
	s.mux.HandleFunc("GET /api/v1/search", s.auth(s.handleSearch))
	s.mux.HandleFunc("GET /api/v1/lyric", s.auth(s.handleLyric))
	s.mux.HandleFunc("POST /api/v1/auto-tag", s.auth(s.handleAutoTag))
	s.mux.HandleFunc("POST /api/v1/apply", s.auth(s.handleApply))
	s.mux.HandleFunc("POST /api/v1/organize", s.auth(s.handleOrganize))
	s.mux.HandleFunc("POST /api/v1/batch-rename", s.auth(s.handleBatchRename))
	s.mux.HandleFunc("GET /api/v1/config", s.auth(s.handleGetConfig))
	s.mux.HandleFunc("PUT /api/v1/config", s.auth(s.handleUpdateConfig))
	s.mux.HandleFunc("POST /api/v1/auto-import/start", s.auth(s.handleStartAutoImport))
	s.mux.HandleFunc("POST /api/v1/auto-import/stop", s.auth(s.handleStopAutoImport))
	s.mux.HandleFunc("POST /api/v1/import", s.auth(s.handleImport))
	s.mux.HandleFunc("GET /api/v1/browse-folder", s.auth(s.handleBrowseFolder))
	s.mux.HandleFunc("/", s.serveWeb)
}

func (s *Server) ListenAndServe() error {
	handler := s.cors(s.logging(s.mux))
	return http.ListenAndServe(fmt.Sprintf(":%d", s.config.Port), handler)
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.config.Token != "" {
			token := r.Header.Get("Authorization")
			if token == "" {
				token = r.URL.Query().Get("token")
			}
			token = strings.TrimPrefix(token, "Bearer ")
			if token != s.config.Token {
				s.sendError(w, http.StatusUnauthorized, "unauthorized: invalid or missing token")
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %v", r.Method, r.URL.Path, time.Since(start))
	})
}

func (s *Server) serveWeb(w http.ResponseWriter, r *http.Request) {
	if s.config.WebFS != (embed.FS{}) {
		sub, err := fs.Sub(s.config.WebFS, "web")
		if err == nil {
			http.FileServer(http.FS(sub)).ServeHTTP(w, r)
			return
		}
	}
	http.FileServer(http.Dir("web")).ServeHTTP(w, r)
}

func (s *Server) sendJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) sendOK(w http.ResponseWriter, data interface{}) {
	s.sendJSON(w, http.StatusOK, Response{OK: true, Data: data})
}

func (s *Server) sendOKWithElapsed(w http.ResponseWriter, data interface{}, start time.Time) {
	s.sendJSON(w, http.StatusOK, Response{
		OK:      true,
		Data:    data,
		Elapsed: time.Since(start).Round(time.Millisecond).String(),
	})
}

func (s *Server) sendError(w http.ResponseWriter, status int, msg string) {
	s.sendJSON(w, status, Response{OK: false, Error: msg})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.sendOK(w, map[string]interface{}{
		"status":         "ok",
		"music_dir":      s.config.MusicDir,
		"ffmpeg":         s.config.FFmpegAvail,
		"ffprobe":        s.config.FFprobeAvail,
	})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	s.sendOK(w, map[string]interface{}{
		"formats": []string{
			"mp3", "flac", "wav", "aiff", "m4a", "mp4",
			"ogg", "opus", "wma", "ape", "wv", "tta",
			"mpc", "dsf", "dff", "aac",
		},
		"read_formats": []string{
			"mp3", "flac", "wav", "aiff", "m4a", "mp4",
			"ogg", "opus", "wma", "ape", "dsf", "aac",
		},
		"write_formats": func() []string {
			f := []string{"mp3"}
			if s.config.FFmpegAvail {
				f = append(f, "flac", "m4a", "mp4", "ogg", "opus", "wma", "wav", "aiff", "dsf", "aac")
			}
			return f
		}(),
		"providers": []string{"netease", "qq", "kugou", "kuwo"},
		"features": []string{
			"tag_read", "tag_write", "folder_browse",
			"cover_read", "cover_write",
			"search", "lyric", "auto_tag", "organize",
		},
		"ffmpeg_available": s.config.FFmpegAvail,
	})
}

func (s *Server) handleBrowseFolder(w http.ResponseWriter, r *http.Request) {
	path, err := dialog.SelectFolder("选择文件夹")
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, "打开文件夹对话框失败: "+err.Error())
		return
	}
	if path == "" {
		s.sendError(w, http.StatusBadRequest, "未选择文件夹")
		return
	}
	s.sendJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true,
		"data": map[string]string{
			"path": path,
		},
	})
}
