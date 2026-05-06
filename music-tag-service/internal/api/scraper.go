package api

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"music-tag-service/internal/scraper"
)

type ScrapedSong = scraper.ScrapedSong

func Search(provider, keyword string) ([]ScrapedSong, error) {
	p, err := scraper.GetProvider(provider)
	if err != nil {
		return nil, err
	}
	return p.Search(keyword)
}

func FetchLyric(provider, id string) (string, error) {
	p, err := scraper.GetProvider(provider)
	if err != nil {
		return "", err
	}
	return p.FetchLyric(id)
}

func matchScore(myValue, uValue string) int {
	myValue = strings.ToLower(strings.ReplaceAll(myValue, " ", ""))
	uValue = strings.ToLower(strings.ReplaceAll(uValue, " ", ""))
	if myValue == "" || uValue == "" {
		return 0
	}
	if myValue == uValue {
		return 2
	}
	if strings.Contains(myValue, uValue) || strings.Contains(uValue, myValue) {
		return 1
	}
	return 0
}

func matchArtist(myValue, uValue string) int {
	if strings.Contains(uValue, ",") {
		parts := strings.Split(uValue, ",")
		score := 0
		if len(parts) > 0 {
			score += matchScore(myValue, strings.ReplaceAll(parts[0], " ", ""))
		}
		if len(parts) > 1 {
			score += matchScore(myValue, strings.ReplaceAll(parts[1], " ", ""))
		}
		return score
	}
	return matchScore(myValue, uValue)
}

type MatchResult struct {
	Song  ScrapedSong `json:"song"`
	Score int         `json:"score"`
}

func matchSong(title, artist, album string, songs []ScrapedSong, selectMode string) *ScrapedSong {
	var bestSong *ScrapedSong
	bestScore := 0

	for i := range songs {
		song := &songs[i]
		titleScore := matchScore(title, song.Name)
		artistScore := matchArtist(func() string {
			if artist != "" {
				return artist
			}
			return title
		}(), song.Artist)
		albumScore := matchScore(func() string {
			if album != "" {
				return album
			}
			return title
		}(), song.Album)

		if artist != "" && artistScore == 0 {
			artistScore = -2
		}
		if artist == "" && artistScore >= 1 {
			if titleScore >= 1 {
				titleScore = 2
			}
		}

		totalScore := titleScore + artistScore + albumScore

		if totalScore >= 3 {
			return song
		}
		if selectMode == "simple" && titleScore == 2 {
			return song
		}

		if titleScore > 0 && totalScore > bestScore {
			bestScore = totalScore
			bestSong = song
		}
	}

	return bestSong
}

func smartTagSearch(title, artist, album string) []MatchResult {
	providers := []string{"qmusic", "netease", "kugou"}
	type providerResult struct {
		songs []ScrapedSong
		err   error
	}

	results := make([]providerResult, len(providers))
	var wg sync.WaitGroup

	for i, provider := range providers {
		wg.Add(1)
		go func(idx int, prov string) {
			defer wg.Done()
			songs, err := Search(prov, title)
			results[idx] = providerResult{songs: songs, err: err}
		}(i, provider)
	}
	wg.Wait()

	var allSongs []MatchResult
	for _, result := range results {
		if result.err != nil {
			continue
		}
		for _, song := range result.songs {
			titleScore := matchScore(title, song.Name)
			artistScore := matchArtist(func() string {
				if artist != "" {
					return artist
				}
				return title
			}(), song.Artist)
			albumScore := matchScore(func() string {
				if album != "" {
					return album
				}
				return title
			}(), song.Album)

			if artist != "" && artistScore == 0 {
				artistScore = -2
			}
			if artist == "" && artistScore >= 1 {
				if titleScore >= 1 {
					titleScore = 2
				}
			}

			score := titleScore + artistScore + albumScore
			if titleScore == 0 {
				continue
			}
			allSongs = append(allSongs, MatchResult{Song: song, Score: score})
		}
	}

	sortMatchResults(allSongs)
	if len(allSongs) > 15 {
		allSongs = allSongs[:15]
	}

	return allSongs
}

func sortMatchResults(results []MatchResult) {
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Score > results[j-1].Score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	provider := r.URL.Query().Get("provider")

	if q == "" {
		s.sendError(w, http.StatusBadRequest, "q parameter is required")
		return
	}
	if provider == "" {
		provider = "netease"
	}

	songs, err := Search(provider, q)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("search error: %v", err))
		return
	}

	s.sendOK(w, map[string]interface{}{
		"query":    q,
		"provider": provider,
		"songs":    songs,
		"count":    len(songs),
	})
}

func (s *Server) handleLyric(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	provider := r.URL.Query().Get("provider")

	if id == "" {
		s.sendError(w, http.StatusBadRequest, "id parameter is required")
		return
	}
	if provider == "" {
		provider = "netease"
	}

	lyric, err := FetchLyric(provider, id)
	if err != nil {
		s.sendError(w, http.StatusInternalServerError, fmt.Sprintf("lyric error: %v", err))
		return
	}

	s.sendOK(w, map[string]interface{}{
		"id":       id,
		"provider": provider,
		"lyric":    lyric,
	})
}
