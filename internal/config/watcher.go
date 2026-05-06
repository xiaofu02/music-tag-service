package config

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const FsnotifyCreate = fsnotify.Create

type WatchEvent struct {
	Path     string
	Op       fsnotify.Op
	Timestamp time.Time
}

type Watcher struct {
	fw         *fsnotify.Watcher
	path       string
	eventCh    chan []WatchEvent
	done       chan struct{}
	mu         sync.Mutex
	isRunning  bool
	knownFiles map[string]time.Time
	scanDone   bool
}

func NewWatcher(path string) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		fw:         fw,
		path:       path,
		eventCh:    make(chan []WatchEvent, 100),
		done:       make(chan struct{}),
		knownFiles: make(map[string]time.Time),
	}

	if err := fw.Add(path); err != nil {
		fw.Close()
		return nil, err
	}

	return w, nil
}

func (w *Watcher) Events() <-chan []WatchEvent {
	return w.eventCh
}

func (w *Watcher) Start() {
	go w.run()
}

func (w *Watcher) Stop() {
	close(w.done)
	w.fw.Close()
}

func (w *Watcher) run() {
	w.scanExistingFiles()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	log.Printf("watcher: started monitoring %s", w.path)

	for {
		select {
		case <-w.done:
			return
		case event, ok := <-w.fw.Events:
			if !ok {
				return
			}
			log.Printf("watcher: received event %v for %s", event.Op, event.Name)
			w.handleEvent(event)
		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			log.Printf("watcher error: %v", err)
		case <-ticker.C:
			w.checkPendingFiles()
		}
	}
}

func (w *Watcher) scanExistingFiles() {
	absPath, err := filepath.Abs(w.path)
	if err != nil {
		log.Printf("watcher: failed to get absolute path: %v", err)
		absPath = w.path
	}

	filepath.Walk(absPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && isAudioFile(path) {
			normalizedPath := filepath.Clean(path)
			w.knownFiles[normalizedPath] = time.Now()
		}
		return nil
	})
	w.scanDone = true
	log.Printf("watcher: scanned %d existing files in %s", len(w.knownFiles), absPath)
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	if event.Op&fsnotify.Create == fsnotify.Create {
		normalizedPath := filepath.Clean(event.Name)
		if isAudioFile(normalizedPath) {
			w.mu.Lock()
			w.knownFiles[normalizedPath] = time.Now()
			w.mu.Unlock()
			log.Printf("watcher: detected new audio file: %s", normalizedPath)
		}
	}
}

func (w *Watcher) checkPendingFiles() {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	var ready []WatchEvent

	for path, addTime := range w.knownFiles {
		if now.After(addTime) {
			if _, err := os.Stat(path); err == nil {
				ready = append(ready, WatchEvent{
					Path:     path,
					Op:       fsnotify.Create,
					Timestamp: addTime,
				})
				log.Printf("watcher: file ready for processing: %s", path)
			}
			delete(w.knownFiles, path)
		}
	}

	if len(ready) > 0 {
		log.Printf("watcher: sending %d events to channel", len(ready))
		select {
		case w.eventCh <- ready:
		default:
			log.Printf("watcher: channel full, dropping events")
		}
	}
}

func isAudioFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	audioExts := []string{".mp3", ".flac", ".wav", ".aiff", ".m4a", ".ogg", ".opus", ".wma", ".ape", ".wv", ".tta", ".mpc", ".dsf", ".dff", ".aac"}
	for _, e := range audioExts {
		if ext == e {
			return true
		}
	}
	return false
}
