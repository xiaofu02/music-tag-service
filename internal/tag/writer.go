package tag

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bogem/id3v2"
)

var coverClient = &http.Client{Timeout: 15 * time.Second}

type TagWriteRequest struct {
	FilePath       string `json:"file_path"`
	Title          string `json:"title,omitempty"`
	Artist         string `json:"artist,omitempty"`
	Album          string `json:"album,omitempty"`
	AlbumArtist    string `json:"album_artist,omitempty"`
	Genre          string `json:"genre,omitempty"`
	Year           int    `json:"year,omitempty"`
	TrackNumber    int    `json:"track_number,omitempty"`
	TrackTotal     int    `json:"track_total,omitempty"`
	DiscNumber     int    `json:"disc_number,omitempty"`
	DiscTotal      int    `json:"disc_total,omitempty"`
	Lyrics         string `json:"lyrics,omitempty"`
	Comment        string `json:"comment,omitempty"`
	CoverBase64    string `json:"cover_base64,omitempty"`
	CoverURL       string `json:"cover_url,omitempty"`
	CoverMime      string `json:"cover_mime,omitempty"`
	RemoveCover    bool   `json:"remove_cover,omitempty"`
	SaveCoverFile  bool   `json:"save_cover_file,omitempty"`
	SaveLyricsFile bool   `json:"save_lyrics_file,omitempty"`
}

type TagWriteResult struct {
	FilePath string `json:"file_path"`
	Success  bool   `json:"success"`
	Message  string `json:"message,omitempty"`
}

func WriteTag(req TagWriteRequest, ffmpegPath string) TagWriteResult {
	if req.CoverBase64 == "" && req.CoverURL != "" && !req.RemoveCover {
		imgData, mimeType, err := downloadCover(req.CoverURL)
		if err == nil && len(imgData) > 0 {
			req.CoverBase64 = base64.StdEncoding.EncodeToString(imgData)
			if req.CoverMime == "" {
				req.CoverMime = mimeType
			}
		}
	}

	ext := strings.ToLower(filepath.Ext(req.FilePath))

	switch ext {
	case ".mp3":
		return writeID3v2(req)
	case ".flac", ".ogg", ".opus", ".wv", ".ape",
		".m4a", ".mp4", ".wma", ".aac", ".dsf", ".dff", ".wav", ".aiff":
		if ffmpegPath != "" {
			return writeWithFFmpeg(req, ffmpegPath)
		}
		return TagWriteResult{
			FilePath: req.FilePath,
			Success:  false,
			Message:  fmt.Sprintf("writing %s tags requires ffmpeg, but ffmpeg is not available", ext),
		}
	default:
		return TagWriteResult{
			FilePath: req.FilePath,
			Success:  false,
			Message:  fmt.Sprintf("unsupported format: %s", ext),
		}
	}
}

func downloadCover(url string) ([]byte, string, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := coverClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, "", err
	}

	mimeType := "image/jpeg"
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		if strings.Contains(ct, "png") {
			mimeType = "image/png"
		} else if strings.Contains(ct, "webp") {
			mimeType = "image/webp"
		}
	} else {
		if _, format, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
			if format == "png" {
				mimeType = "image/png"
			}
		}
	}

	return data, mimeType, nil
}

func writeID3v2(req TagWriteRequest) TagWriteResult {
	mp3tag, err := id3v2.Open(req.FilePath, id3v2.Options{Parse: true})
	if err != nil {
		return TagWriteResult{FilePath: req.FilePath, Success: false, Message: fmt.Sprintf("open file: %v", err)}
	}
	defer mp3tag.Close()

	mp3tag.SetVersion(4)
	mp3tag.SetDefaultEncoding(id3v2.EncodingUTF8)

	if req.Title != "" {
		mp3tag.SetTitle(req.Title)
	}
	if req.Artist != "" {
		mp3tag.SetArtist(req.Artist)
	}
	if req.Album != "" {
		mp3tag.SetAlbum(req.Album)
	}
	if req.AlbumArtist != "" {
		mp3tag.AddTextFrame("TPE2", id3v2.EncodingUTF8, req.AlbumArtist)
	}
	if req.Genre != "" {
		mp3tag.SetGenre(req.Genre)
	}
	if req.Year != 0 {
		mp3tag.SetYear(strconv.Itoa(req.Year))
	}
	if req.TrackNumber != 0 {
		trackStr := fmt.Sprintf("%d", req.TrackNumber)
		if req.TrackTotal != 0 {
			trackStr = fmt.Sprintf("%d/%d", req.TrackNumber, req.TrackTotal)
		}
		mp3tag.AddTextFrame("TRCK", id3v2.EncodingUTF8, trackStr)
	}
	if req.DiscNumber != 0 {
		discStr := fmt.Sprintf("%d", req.DiscNumber)
		if req.DiscTotal != 0 {
			discStr = fmt.Sprintf("%d/%d", req.DiscNumber, req.DiscTotal)
		}
		mp3tag.AddTextFrame("TPOS", id3v2.EncodingUTF8, discStr)
	}
	if req.Comment != "" {
		mp3tag.AddCommentFrame(id3v2.CommentFrame{
			Encoding:    id3v2.EncodingUTF8,
			Language:    "chi",
			Description: "",
			Text:        req.Comment,
		})
	}
	if req.Lyrics != "" {
		mp3tag.AddUnsynchronisedLyricsFrame(id3v2.UnsynchronisedLyricsFrame{
			Encoding:          id3v2.EncodingUTF8,
			Language:          "chi",
			ContentDescriptor: "",
			Lyrics:            req.Lyrics,
		})
	}

	if req.RemoveCover {
		mp3tag.DeleteFrames("APIC")
	} else if req.CoverBase64 != "" {
		imgData, err := base64.StdEncoding.DecodeString(req.CoverBase64)
		if err != nil {
			return TagWriteResult{FilePath: req.FilePath, Success: false, Message: fmt.Sprintf("decode cover base64: %v", err)}
		}

		mimeType := "image/jpeg"
		if req.CoverMime != "" {
			mimeType = req.CoverMime
		} else if _, format, err := image.DecodeConfig(bytes.NewReader(imgData)); err == nil {
			if format == "png" {
				mimeType = "image/png"
			}
		}

		pic := id3v2.PictureFrame{
			Encoding:    id3v2.EncodingUTF8,
			MimeType:    mimeType,
			PictureType: id3v2.PTFrontCover,
			Description: "Cover",
			Picture:     imgData,
		}
		mp3tag.DeleteFrames("APIC")
		mp3tag.AddAttachedPicture(pic)

		if req.SaveCoverFile {
			saveCoverToFile(req.FilePath, req.Album, imgData, mimeType)
		}
	}

	if err := mp3tag.Save(); err != nil {
		return TagWriteResult{FilePath: req.FilePath, Success: false, Message: fmt.Sprintf("save tags: %v", err)}
	}

	if req.SaveLyricsFile && req.Lyrics != "" {
		saveLyricsToFile(req.FilePath, req.Lyrics)
	}

	return TagWriteResult{FilePath: req.FilePath, Success: true}
}

func writeWithFFmpeg(req TagWriteRequest, ffmpegPath string) TagWriteResult {
	ext := filepath.Ext(req.FilePath)
	tmpFile := req.FilePath + ".tmp" + ext

	var args []string
	if req.CoverBase64 != "" && !req.RemoveCover {
		imgData, err := base64.StdEncoding.DecodeString(req.CoverBase64)
		if err == nil && len(imgData) > 0 {
			coverPath := req.FilePath + ".cover_tmp"
			if err := os.WriteFile(coverPath, imgData, 0644); err == nil {
				defer os.Remove(coverPath)
				args = buildFFmpegArgsWithCover(req, coverPath, tmpFile)
			} else {
				args = buildFFmpegArgs(req, tmpFile)
			}
		} else {
			args = buildFFmpegArgs(req, tmpFile)
		}
	} else {
		args = buildFFmpegArgs(req, tmpFile)
	}

	cmd := exec.Command(ffmpegPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		os.Remove(tmpFile)
		return TagWriteResult{
			FilePath: req.FilePath,
			Success:  false,
			Message:  fmt.Sprintf("ffmpeg error: %v, stderr: %s", err, stderr.String()),
		}
	}

	if err := os.Remove(req.FilePath); err != nil {
		os.Remove(tmpFile)
		return TagWriteResult{
			FilePath: req.FilePath,
			Success:  false,
			Message:  fmt.Sprintf("remove original: %v", err),
		}
	}

	if err := os.Rename(tmpFile, req.FilePath); err != nil {
		return TagWriteResult{
			FilePath: req.FilePath,
			Success:  false,
			Message:  fmt.Sprintf("rename temp file: %v", err),
		}
	}

	if req.SaveLyricsFile && req.Lyrics != "" {
		saveLyricsToFile(req.FilePath, req.Lyrics)
	}

	if req.SaveCoverFile && req.CoverBase64 != "" && !req.RemoveCover {
		imgData, _ := base64.StdEncoding.DecodeString(req.CoverBase64)
		if len(imgData) > 0 {
			mimeType := "image/jpeg"
			if req.CoverMime != "" {
				mimeType = req.CoverMime
			}
			saveCoverToFile(req.FilePath, req.Album, imgData, mimeType)
		}
	}

	return TagWriteResult{FilePath: req.FilePath, Success: true}
}

func buildFFmpegArgs(req TagWriteRequest, tmpFile string) []string {
	args := []string{"-i", req.FilePath}

	if req.CoverBase64 == "" || req.RemoveCover {
		args = append(args, "-c", "copy")
	}

	if req.Title != "" {
		args = append(args, "-metadata", fmt.Sprintf("title=%s", req.Title))
	}
	if req.Artist != "" {
		args = append(args, "-metadata", fmt.Sprintf("artist=%s", req.Artist))
	}
	if req.Album != "" {
		args = append(args, "-metadata", fmt.Sprintf("album=%s", req.Album))
	}
	if req.AlbumArtist != "" {
		args = append(args, "-metadata", fmt.Sprintf("album_artist=%s", req.AlbumArtist))
	}
	if req.Genre != "" {
		args = append(args, "-metadata", fmt.Sprintf("genre=%s", req.Genre))
	}
	if req.Year != 0 {
		args = append(args, "-metadata", fmt.Sprintf("date=%d", req.Year))
	}
	if req.TrackNumber != 0 {
		args = append(args, "-metadata", fmt.Sprintf("track=%d", req.TrackNumber))
	}
	if req.DiscNumber != 0 {
		args = append(args, "-metadata", fmt.Sprintf("disc=%d", req.DiscNumber))
	}
	if req.Comment != "" {
		args = append(args, "-metadata", fmt.Sprintf("comment=%s", req.Comment))
	}

	args = append(args, "-y", tmpFile)
	return args
}

func buildFFmpegArgsWithCover(req TagWriteRequest, coverPath string, tmpFile string) []string {
	args := []string{
		"-i", req.FilePath,
		"-i", coverPath,
	}

	ext := strings.ToLower(filepath.Ext(req.FilePath))
	if ext == ".flac" {
		args = append(args,
			"-c:a", "copy",
			"-c:v", "mjpeg",
			"-map", "0:a",
			"-map", "1:v",
			"-disposition:v", "attached_pic",
		)
	} else {
		args = append(args,
			"-map", "0:a",
			"-map", "1:v",
			"-c:v", "copy",
			"-c:a", "copy",
			"-disposition:v", "attached_pic",
		)
	}

	if req.Title != "" {
		args = append(args, "-metadata", fmt.Sprintf("title=%s", req.Title))
	}
	if req.Artist != "" {
		args = append(args, "-metadata", fmt.Sprintf("artist=%s", req.Artist))
	}
	if req.Album != "" {
		args = append(args, "-metadata", fmt.Sprintf("album=%s", req.Album))
	}
	if req.AlbumArtist != "" {
		args = append(args, "-metadata", fmt.Sprintf("album_artist=%s", req.AlbumArtist))
	}
	if req.Genre != "" {
		args = append(args, "-metadata", fmt.Sprintf("genre=%s", req.Genre))
	}
	if req.Year != 0 {
		args = append(args, "-metadata", fmt.Sprintf("date=%d", req.Year))
	}
	if req.TrackNumber != 0 {
		args = append(args, "-metadata", fmt.Sprintf("track=%d", req.TrackNumber))
	}
	if req.DiscNumber != 0 {
		args = append(args, "-metadata", fmt.Sprintf("disc=%d", req.DiscNumber))
	}
	if req.Comment != "" {
		args = append(args, "-metadata", fmt.Sprintf("comment=%s", req.Comment))
	}

	args = append(args, "-y", tmpFile)
	return args
}

func saveCoverToFile(musicPath, album string, imgData []byte, mimeType string) {
	dir := filepath.Dir(musicPath)
	ext := ".jpg"
	if mimeType == "image/png" {
		ext = ".png"
	}

	coverName := "cover"
	if album != "" {
		safeName := strings.Map(func(r rune) rune {
			if r == '\\' || r == '/' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|' {
				return '_'
			}
			return r
		}, album)
		coverName = fmt.Sprintf("cover-%s", safeName)
	}

	coverPath := filepath.Join(dir, coverName+ext)
	if _, err := os.Stat(coverPath); os.IsNotExist(err) {
		os.WriteFile(coverPath, imgData, 0644)
	}
}

func saveLyricsToFile(musicPath, lyrics string) {
	dir := filepath.Dir(musicPath)
	base := strings.TrimSuffix(filepath.Base(musicPath), filepath.Ext(musicPath))
	lrcPath := filepath.Join(dir, base+".lrc")
	os.WriteFile(lrcPath, []byte(lyrics), 0644)
}
