package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"music-tag-service/internal/api"
	"music-tag-service/internal/config"
)

func main() {
	port := flag.Int("port", 8080, "HTTP server port")
	musicDir := flag.String("music-dir", "", "Root music directory (default: user's Music folder)")
	token := flag.String("token", "", "API authentication token (optional)")
	flag.Parse()

	exePath, _ := os.Executable()
	cfgDir := filepath.Dir(exePath)
	config.SetConfigDir(cfgDir)
	cfgManager, err := config.NewManager(exePath)
	if err != nil {
		log.Printf("Warning: failed to load config: %v", err)
	}

	if *musicDir == "" {
		*musicDir = os.Getenv("MUSIC_DIR")
	}
	if *musicDir == "" {
		*musicDir = defaultMusicDir()
	}

	info, err := os.Stat(*musicDir)
	if err != nil || !info.IsDir() {
		msg := fmt.Sprintf("Error: directory not found: %s\n\nPlease provide a valid music directory:\n  music-tag-service.exe -music-dir \"D:\\Music\"", *musicDir)
		fmt.Println(msg)
		pauseOnError()
		os.Exit(1)
	}

	ffmpegPath, ffmpegAvail := findFFmpeg("ffmpeg")
	ffprobePath, ffprobeAvail := findFFmpeg("ffprobe")

	if !ffmpegAvail {
		log.Println("Warning: ffmpeg not found, tag writing for non-MP3 formats will be unavailable")
	}
	if !ffprobeAvail {
		log.Println("Warning: ffprobe not found, extended tag info will be limited")
	}

	srv := api.NewServer(api.Config{
		Port:         *port,
		MusicDir:     *musicDir,
		Token:        *token,
		FFmpegPath:   ffmpegPath,
		FFprobePath:  ffprobePath,
		FFmpegAvail:  ffmpegAvail,
		FFprobeAvail: ffprobeAvail,
	}, cfgManager)

	fmt.Println("============================================")
	fmt.Println("  Music Tag Service")
	fmt.Println("============================================")
	fmt.Printf("  URL:        http://localhost:%d\n", *port)
	fmt.Printf("  Music Dir:  %s\n", *musicDir)
	if *token != "" {
		fmt.Println("  Auth:       enabled (token)")
	} else {
		fmt.Println("  Auth:       disabled")
	}
	if ffmpegAvail {
		fmt.Printf("  FFmpeg:     %s\n", ffmpegPath)
	} else {
		fmt.Println("  FFmpeg:     not found (将ffmpeg.exe放入ffmpeg/文件夹即可)")
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()

	if cfgManager != nil {
		cfg := cfgManager.Get()
		fmt.Printf("  Auto-Import: %v\n", cfg.AutoImport.Enabled)
		if cfg.AutoImport.Enabled && cfg.AutoImport.WatchPath != "" {
			fmt.Printf("  Watch:      %s\n", cfg.AutoImport.WatchPath)
			go func() {
				log.Println("Starting auto-import from config...")
				srv.StartAutoImport(api.AutoImportConfig{
					WatchPath:   cfg.AutoImport.WatchPath,
					Concurrency: cfg.AutoImport.Concurrency,
					AutoTag:     true,
					Providers:   cfg.AutoImport.Providers,
					Mode:        cfg.AutoImport.Mode,
					Overwrite:   cfg.AutoImport.Overwrite,
				})
			}()
		}
	}
	fmt.Println("============================================")
	fmt.Println("  Press Ctrl+C to stop")
	fmt.Println("============================================")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	fmt.Println("\nShutting down...")
}

func defaultMusicDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	candidates := []string{
		filepath.Join(home, "Music"),
		filepath.Join(home, "music"),
		filepath.Join(home, "My Music"),
	}

	for _, dir := range candidates {
		info, err := os.Stat(dir)
		if err == nil && info.IsDir() {
			return dir
		}
	}

	return ""
}

func findFFmpeg(name string) (string, bool) {
	exeName := name
	if runtime.GOOS == "windows" {
		exeName = name + ".exe"
	}

	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)

	localPath := filepath.Join(exeDir, "ffmpeg", exeName)
	if info, err := os.Stat(localPath); err == nil && !info.IsDir() {
		return localPath, true
	}

	if runtime.GOOS == "windows" {
		path, err := exec.LookPath(exeName)
		if err == nil {
			return path, true
		}
	}
	path, err := exec.LookPath(name)
	if err == nil {
		return path, true
	}
	return "", false
}

func pauseOnError() {
	if runtime.GOOS == "windows" {
		fmt.Println("\nPress Enter to exit...")
		var b [1]byte
		os.Stdin.Read(b[:])
	}
}
