package tag

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bogem/id3v2"
	dtag "github.com/dhowden/tag"
)

type TagInfo struct {
	FilePath    string  `json:"file_path"`
	FileName    string  `json:"file_name"`
	Title       string  `json:"title"`
	Artist      string  `json:"artist"`
	Album       string  `json:"album"`
	AlbumArtist string  `json:"album_artist"`
	Genre       string  `json:"genre"`
	Year        int     `json:"year"`
	TrackNumber int     `json:"track_number"`
	TrackTotal  int     `json:"track_total"`
	DiscNumber  int     `json:"disc_number"`
	DiscTotal   int     `json:"disc_total"`
	Lyrics      string  `json:"lyrics"`
	Comment     string  `json:"comment"`
	Duration    float64 `json:"duration"`
	BitRate     int     `json:"bit_rate"`
	Format      string  `json:"format"`
	FileType    string  `json:"file_type"`
	FileSize    int64   `json:"file_size"`
	HasCover    bool    `json:"has_cover"`
	CoverMime   string  `json:"cover_mime,omitempty"`
	CoverBase64 string  `json:"cover_base64,omitempty"`
}

func ReadTag(filePath string) (*TagInfo, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}

	return readTagFromFile(f, filePath, fi.Size(), true)
}

func ReadTagNoCover(filePath string) (*TagInfo, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}

	return readTagFromFile(f, filePath, fi.Size(), false)
}

func readTagFromFile(f *os.File, filePath string, fileSize int64, includeCover bool) (*TagInfo, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	if ext == ".mp3" {
		info, err := readID3v2Tag(filePath, fileSize, includeCover)
		if err == nil {
			return info, nil
		}
	}

	meta, err := dtag.ReadFrom(f)
	if err != nil {
		if ext == ".mp3" {
			return readID3v2Tag(filePath, fileSize, includeCover)
		}
		return nil, fmt.Errorf("read tags: %w", err)
	}

	trackNum, trackTotal := meta.Track()
	discNum, discTotal := meta.Disc()

	info := &TagInfo{
		FilePath:    filePath,
		FileName:    filepath.Base(filePath),
		Title:       meta.Title(),
		Artist:      meta.Artist(),
		Album:       meta.Album(),
		AlbumArtist: meta.AlbumArtist(),
		Genre:       meta.Genre(),
		Year:        meta.Year(),
		TrackNumber: trackNum,
		TrackTotal:  trackTotal,
		DiscNumber:  discNum,
		DiscTotal:   discTotal,
		Lyrics:      meta.Lyrics(),
		Comment:     meta.Comment(),
		Format:      string(meta.Format()),
		FileType:    string(meta.FileType()),
		FileSize:    fileSize,
	}

	pic := meta.Picture()
	if pic != nil {
		info.HasCover = true
		info.CoverMime = pic.MIMEType
		if includeCover {
			info.CoverBase64 = base64.StdEncoding.EncodeToString(pic.Data)
		}
	}

	if info.Duration > 0 && fileSize > 0 {
		info.BitRate = int(float64(fileSize) / info.Duration * 8 / 1000)
	}

	if info.Title == "" {
		ext := filepath.Ext(info.FileName)
		info.Title = strings.TrimSuffix(info.FileName, ext)
	}

	return info, nil
}

func readID3v2Tag(filePath string, fileSize int64, includeCover bool) (*TagInfo, error) {
	mp3tag, err := id3v2.Open(filePath, id3v2.Options{Parse: true})
	if err != nil {
		return nil, fmt.Errorf("read id3v2: %w", err)
	}
	defer mp3tag.Close()

	info := &TagInfo{
		FilePath:    filePath,
		FileName:    filepath.Base(filePath),
		Title:       mp3tag.Title(),
		Artist:      mp3tag.Artist(),
		Album:       mp3tag.Album(),
		Genre:       mp3tag.Genre(),
		Format:      "ID3v2." + string(mp3tag.Version()),
		FileType:    "MP3",
		FileSize:    fileSize,
	}

	yearStr := mp3tag.Year()
	if yearStr != "" {
		info.Year, _ = strconv.Atoi(yearStr)
	}

	trackNum, trackTotal := parseTrackDisc(mp3tag.GetTextFrame("TRCK").Text)
	info.TrackNumber = trackNum
	info.TrackTotal = trackTotal

	discNum, discTotal := parseTrackDisc(mp3tag.GetTextFrame("TPOS").Text)
	info.DiscNumber = discNum
	info.DiscTotal = discTotal

	info.AlbumArtist = mp3tag.GetTextFrame("TPE2").Text

	frames := mp3tag.GetFrames("USLT")
	for _, frame := range frames {
		if uslt, ok := frame.(id3v2.UnsynchronisedLyricsFrame); ok {
			if uslt.Lyrics != "" {
				info.Lyrics = uslt.Lyrics
				break
			}
		}
	}

	commentFrames := mp3tag.GetFrames("COMM")
	for _, frame := range commentFrames {
		if comm, ok := frame.(id3v2.CommentFrame); ok {
			if comm.Text != "" {
				info.Comment = comm.Text
				break
			}
		}
	}

	if includeCover {
		picFrames := mp3tag.GetFrames("APIC")
		for _, frame := range picFrames {
			if pic, ok := frame.(id3v2.PictureFrame); ok {
				info.HasCover = true
				info.CoverMime = pic.MimeType
				if len(pic.Picture) > 0 {
					info.CoverBase64 = base64.StdEncoding.EncodeToString(pic.Picture)
				}
				break
			}
		}
	} else {
		picFrames := mp3tag.GetFrames("APIC")
		for _, frame := range picFrames {
			if pic, ok := frame.(id3v2.PictureFrame); ok {
				info.HasCover = true
				info.CoverMime = pic.MimeType
				break
			}
		}
	}

	if info.Title == "" {
		ext := filepath.Ext(info.FileName)
		info.Title = strings.TrimSuffix(info.FileName, ext)
	}

	return info, nil
}

func parseTrackDisc(val string) (num, total int) {
	val = strings.TrimSpace(val)
	if val == "" {
		return 0, 0
	}
	parts := strings.Split(val, "/")
	if len(parts) >= 1 {
		num, _ = strconv.Atoi(strings.TrimSpace(parts[0]))
	}
	if len(parts) >= 2 {
		total, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
	}
	return
}

func IsAudioFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	audioExts := map[string]bool{
		".mp3": true, ".flac": true, ".wav": true, ".aiff": true,
		".aif": true, ".m4a": true, ".mp4": true, ".ogg": true,
		".opus": true, ".wma": true, ".ape": true, ".wv": true,
		".tta": true, ".mpc": true, ".dsf": true, ".dff": true,
		".aac": true,
	}
	return audioExts[ext]
}

func ExtractCover(filePath string) ([]byte, string, error) {
	ext := strings.ToLower(filepath.Ext(filePath))

	if ext == ".mp3" {
		return extractCoverID3v2(filePath)
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	meta, err := dtag.ReadFrom(f)
	if err != nil {
		if ext == ".mp3" {
			return extractCoverID3v2(filePath)
		}
		return nil, "", err
	}

	pic := meta.Picture()
	if pic == nil {
		return nil, "", fmt.Errorf("no cover art found")
	}

	return pic.Data, pic.MIMEType, nil
}

func extractCoverID3v2(filePath string) ([]byte, string, error) {
	mp3tag, err := id3v2.Open(filePath, id3v2.Options{Parse: true})
	if err != nil {
		return nil, "", err
	}
	defer mp3tag.Close()

	picFrames := mp3tag.GetFrames("APIC")
	for _, frame := range picFrames {
		if pic, ok := frame.(id3v2.PictureFrame); ok {
			if len(pic.Picture) > 0 {
				return pic.Picture, pic.MimeType, nil
			}
		}
	}

	return nil, "", fmt.Errorf("no cover art found")
}

func ReadTagFromReader(r io.ReadSeeker, fileName string, fileSize int64) (*TagInfo, error) {
	meta, err := dtag.ReadFrom(r)
	if err != nil {
		return nil, fmt.Errorf("read tags: %w", err)
	}

	trackNum, trackTotal := meta.Track()
	discNum, discTotal := meta.Disc()

	info := &TagInfo{
		FilePath:    fileName,
		FileName:    filepath.Base(fileName),
		Title:       meta.Title(),
		Artist:      meta.Artist(),
		Album:       meta.Album(),
		AlbumArtist: meta.AlbumArtist(),
		Genre:       meta.Genre(),
		Year:        meta.Year(),
		TrackNumber: trackNum,
		TrackTotal:  trackTotal,
		DiscNumber:  discNum,
		DiscTotal:   discTotal,
		Lyrics:      meta.Lyrics(),
		Comment:     meta.Comment(),
		Format:      string(meta.Format()),
		FileType:    string(meta.FileType()),
		FileSize:    fileSize,
	}

	if info.Duration > 0 && fileSize > 0 {
		info.BitRate = int(float64(fileSize) / info.Duration * 8 / 1000)
	}

	pic := meta.Picture()
	if pic != nil {
		info.HasCover = true
		info.CoverMime = pic.MIMEType
	}

	if info.Title == "" {
		ext := filepath.Ext(info.FileName)
		info.Title = strings.TrimSuffix(info.FileName, ext)
	}

	return info, nil
}
