package scraper

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ScrapedSong struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Artist    string `json:"artist"`
	ArtistID  string `json:"artist_id"`
	Album     string `json:"album"`
	AlbumID   string `json:"album_id"`
	AlbumImg  string `json:"album_img"`
	Year      string `json:"year"`
	Provider  string `json:"provider"`
}

type Provider interface {
	Name() string
	Search(keyword string) ([]ScrapedSong, error)
	FetchLyric(id string) (string, error)
}

var httpClient = &http.Client{
	Timeout: 15 * time.Second,
}

func GetProvider(name string) (Provider, error) {
	switch name {
	case "netease":
		return &NeteaseProvider{}, nil
	case "qmusic", "qq":
		return &QQProvider{}, nil
	case "kugou":
		return &KugouProvider{}, nil
	case "kuwo":
		return NewKuwoProvider(), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", name)
	}
}

func AllProviders() []string {
	return []string{"netease", "qmusic", "kugou", "kuwo"}
}

func safeNumber(v interface{}) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case json.Number:
		s := n.String()
		if s == "" {
			return "0"
		}
		return s
	case string:
		if n == "" {
			return "0"
		}
		return n
	case int:
		return strconv.Itoa(n)
	case nil:
		return "0"
	default:
		return fmt.Sprintf("%v", n)
	}
}

// ==================== Netease ====================

type NeteaseProvider struct{}

func (p *NeteaseProvider) Name() string { return "netease" }

func (p *NeteaseProvider) Search(keyword string) ([]ScrapedSong, error) {
	apiURL := fmt.Sprintf("https://music.163.com/api/search/get/web?s=%s&type=1&limit=10&offset=0",
		url.QueryEscape(keyword))

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://music.163.com/")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("netease search: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("netease parse: %w", err)
	}

	resultVal, ok := raw["result"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("netease: no result field")
	}

	songsVal, ok := resultVal["songs"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("netease: no songs field")
	}

	var songs []ScrapedSong
	var songIDs []string
	for _, item := range songsVal {
		song, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		id := safeNumber(song["id"])
		name, _ := song["name"].(string)

		var artistNames []string
		artistID := ""
		if artistsVal, ok := song["artists"].([]interface{}); ok {
			for _, a := range artistsVal {
				if artist, ok := a.(map[string]interface{}); ok {
					if n, ok := artist["name"].(string); ok {
						artistNames = append(artistNames, n)
					}
				}
			}
			if len(artistsVal) > 0 {
				if artist, ok := artistsVal[0].(map[string]interface{}); ok {
					artistID = safeNumber(artist["id"])
				}
			}
		}

		albumName := ""
		albumID := ""
		if albumVal, ok := song["album"].(map[string]interface{}); ok {
			albumName, _ = albumVal["name"].(string)
			albumID = safeNumber(albumVal["id"])
		}

		year := ""
		if pt, ok := song["publishTime"].(float64); ok && pt > 0 {
			year = time.Unix(int64(pt)/1000, 0).Format("2006")
		}

		songs = append(songs, ScrapedSong{
			ID:       id,
			Name:     name,
			Artist:   strings.Join(artistNames, ","),
			ArtistID: artistID,
			Album:    albumName,
			AlbumID:  albumID,
			Year:     year,
			Provider: "netease",
		})
		songIDs = append(songIDs, id)
	}

	picMap := p.batchFetchPicUrls(songIDs)
	for i := range songs {
		if pic, ok := picMap[songs[i].ID]; ok {
			songs[i].AlbumImg = pic
		}
	}

	return songs, nil
}

func (p *NeteaseProvider) batchFetchPicUrls(ids []string) map[string]string {
	result := make(map[string]string)
	if len(ids) == 0 {
		return result
	}

	idsJSON, _ := json.Marshal(ids)
	apiURL := fmt.Sprintf("https://music.163.com/api/song/detail/?id=0&ids=%s", url.QueryEscape(string(idsJSON)))

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://music.163.com/")

	resp, err := httpClient.Do(req)
	if err != nil {
		return result
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var detailResp struct {
		Songs []struct {
			ID     int    `json:"id"`
			Album  struct {
				PicURL string `json:"picUrl"`
			} `json:"album"`
		} `json:"songs"`
	}

	if err := json.Unmarshal(body, &detailResp); err != nil {
		return result
	}

	for _, s := range detailResp.Songs {
		if s.Album.PicURL != "" {
			result[strconv.Itoa(s.ID)] = s.Album.PicURL
		}
	}

	return result
}

func (p *NeteaseProvider) FetchLyric(id string) (string, error) {
	apiURL := fmt.Sprintf("https://music.163.com/api/song/lyric?lv=-1&kv=-1&tv=-1&id=%s", id)

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://music.163.com/")
	req.Header.Set("Cookie", "MUSIC_U=; os=pc;")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("netease lyric: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Lrc struct {
			Lyric string `json:"lyric"`
		} `json:"lrc"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("netease lyric parse: %w", err)
	}

	return result.Lrc.Lyric, nil
}

// ==================== QQ Music ====================

type QQProvider struct{}

func (p *QQProvider) Name() string { return "qmusic" }

func (p *QQProvider) Search(keyword string) ([]ScrapedSong, error) {
	apiURL := "https://u.y.qq.com/cgi-bin/musicu.fcg"

	payload := map[string]interface{}{
		"comm": map[string]interface{}{
			"ct": 19, "cv": "80600",
		},
		"music.search.SearchCgiService.DoSearchForQQMusicDesktop": map[string]interface{}{
			"module": "music.search.SearchCgiService",
			"method": "DoSearchForQQMusicDesktop",
			"param": map[string]interface{}{
				"query":        keyword,
				"num_per_page": 10,
				"page_num":     1,
			},
		},
	}

	jsonData, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", apiURL, strings.NewReader(string(jsonData)))
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("User-Agent", "QQ%E9%9F%B3%E4%B9%90/73222")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qq search: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("qq parse: %w", err)
	}

	searchKey := "music.search.SearchCgiService.DoSearchForQQMusicDesktop"
	searchResult, ok := result[searchKey].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("qq search: unexpected response format")
	}

	data, ok := searchResult["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("qq search: no data field")
	}

	body_ := data["body"].(map[string]interface{})
	songList := body_["song"].(map[string]interface{})
	list, ok := songList["list"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("qq search: no song list")
	}

	var songs []ScrapedSong
	for _, item := range list {
		song, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		title, _ := song["title"].(string)
		mid, _ := song["mid"].(string)

		singers, ok := song["singer"].([]interface{})
		artistNames := make([]string, 0)
		artistMids := make([]string, 0)
		if ok {
			for _, s := range singers {
				if singer, ok := s.(map[string]interface{}); ok {
					if name, ok := singer["name"].(string); ok {
						artistNames = append(artistNames, name)
					}
					if mid, ok := singer["mid"].(string); ok {
						artistMids = append(artistMids, mid)
					}
				}
			}
		}

		albumName := ""
		albumMid := ""
		albumID := ""
		if album, ok := song["album"].(map[string]interface{}); ok {
			albumName, _ = album["title"].(string)
			albumMid, _ = album["mid"].(string)
			albumID = safeNumber(album["id"])
		}

		coverURL := ""
		if albumMid != "" {
			coverURL = fmt.Sprintf("http://y.qq.com/music/photo_new/T002R300x300M000%s.jpg", albumMid)
		}

		artistID := ""
		if len(artistMids) > 0 {
			artistID = artistMids[0]
		}

		songs = append(songs, ScrapedSong{
			ID:       mid,
			Name:     title,
			Artist:   strings.Join(artistNames, ","),
			ArtistID: artistID,
			Album:    albumName,
			AlbumID:  albumID,
			AlbumImg: coverURL,
			Provider: "qmusic",
		})
	}

	return songs, nil
}

func (p *QQProvider) FetchLyric(id string) (string, error) {
	apiURL := fmt.Sprintf("https://c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg?g_tk=5381&json=1&format=json&inCharset=utf-8&outCharset=utf-8&notice=0&platform=h5&needNewCode=1&ct=121&cv=0&songmid=%s", id)

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 9_1 like Mac OS X) AppleWebKit/601.1.46 (KHTML, like Gecko) Version/9.0 Mobile/13B143 Safari/600.1")
	req.Header.Set("Referer", "https://y.qq.com/portal/player.html")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("qq lyric: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Retcode int `json:"retcode"`
		Code    int `json:"code"`
		Subcode int `json:"subcode"`
		Lyric   string `json:"lyric"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("qq lyric parse: %w", err)
	}

	if result.Retcode != 0 && result.Code != 0 {
		return "", nil
	}

	lyricB64 := result.Lyric
	if lyricB64 == "" {
		return "", nil
	}

	decoded, err := base64.StdEncoding.DecodeString(lyricB64)
	if err != nil {
		return lyricB64, nil
	}
	return string(decoded), nil
}

// ==================== Kugou ====================

type KugouProvider struct{}

func (p *KugouProvider) Name() string { return "kugou" }

func kugouSignature(text string) string {
	h := md5.New()
	io.WriteString(h, text)
	return strings.ToUpper(fmt.Sprintf("%x", h.Sum(nil)))
}

func (p *KugouProvider) Search(keyword string) ([]ScrapedSong, error) {
	millis := strconv.FormatInt(time.Now().UnixMilli(), 10)
	keyCode := "NVPh5oo715z5DIWAeQlhMDsWXXQV4hwtbitrate=0clienttime={time}clientver=2000dfid=-inputtype=0iscorrection=1isfuzzy=0keyword={keyword}mid={time}page=1pagesize=10platform=WebFilterprivilege_filter=0srcappid=2919tag=emuserid=-1uuid={time}NVPh5oo715z5DIWAeQlhMDsWXXQV4hwt"
	sigStr := strings.ReplaceAll(keyCode, "{time}", millis)
	sigStr = strings.ReplaceAll(sigStr, "{keyword}", keyword)
	signature := kugouSignature(sigStr)

	apiURL := fmt.Sprintf("https://complexsearch.kugou.com/v2/search/song?keyword=%s&page=1&pagesize=10&bitrate=0&isfuzzy=0&tag=em&inputtype=0&platform=WebFilter&userid=-1&clientver=2000&iscorrection=1&privilege_filter=0&srcappid=2919&clienttime=%s&mid=%s&uuid=%s&dfid=-&signature=%s",
		url.QueryEscape(keyword), millis, millis, millis, signature)

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kugou search: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("kugou parse: %w", err)
	}

	dataVal, ok := raw["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("kugou: no data field")
	}

	listsVal, ok := dataVal["lists"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("kugou: no lists field")
	}

	var songs []ScrapedSong
	for _, item := range listsVal {
		song, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		hash, _ := song["FileHash"].(string)
		name, _ := song["SongName"].(string)
		singerName, _ := song["SingerName"].(string)
		singerID := safeNumber(song["SingerId"])
		albumName, _ := song["AlbumName"].(string)
		albumID := safeNumber(song["AlbumID"])
		image, _ := song["Image"].(string)
		publishTime, _ := song["PublishTime"].(string)

		artistName := strings.ReplaceAll(singerName, "<em>", "")
		artistName = strings.ReplaceAll(artistName, "</em>", "")

		songName := strings.ReplaceAll(name, "<em>", "")
		songName = strings.ReplaceAll(songName, "</em>", "")

		coverURL := ""
		if image != "" {
			coverURL = strings.ReplaceAll(image, "{size}", "150")
		}

		songs = append(songs, ScrapedSong{
			ID:       hash,
			Name:     songName,
			Artist:   artistName,
			ArtistID: singerID,
			Album:    albumName,
			AlbumID:  albumID,
			AlbumImg: coverURL,
			Year:     publishTime,
			Provider: "kugou",
		})
	}

	return songs, nil
}

func (p *KugouProvider) FetchLyric(id string) (string, error) {
	apiURL := fmt.Sprintf("https://www.kugou.com/yy/index.php?r=play/getdata&hash=%s", id)

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://www.kugou.com/")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("kugou lyric: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Data struct {
			Lyrics string `json:"lyrics"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("kugou lyric parse: %w", err)
	}

	lyrics := result.Data.Lyrics
	lyrics = strings.ReplaceAll(lyrics, "\r\n", "\n")
	return lyrics, nil
}

// ==================== Kuwo ====================

type KuwoProvider struct{}

func NewKuwoProvider() *KuwoProvider {
	return &KuwoProvider{}
}

func (p *KuwoProvider) Name() string { return "kuwo" }

func (p *KuwoProvider) Search(keyword string) ([]ScrapedSong, error) {
	apiURL := fmt.Sprintf("https://search.kuwo.cn/r.s?client=kt&all=%s&ft=music&pn=0&rn=10&rformat=json&encoding=utf8&vipver=1",
		url.QueryEscape(keyword))

	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://kuwo.cn/")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kuwo search: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	body = bytes.ReplaceAll(body, []byte("'"), []byte("\""))

	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("kuwo parse: %w", err)
	}

	abslistVal, ok := raw["abslist"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("kuwo: no abslist field")
	}

	var songs []ScrapedSong
	for _, item := range abslistVal {
		song, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		musicRID, _ := song["MUSICRID"].(string)
		rid := strings.TrimPrefix(musicRID, "MUSIC_")

		name, _ := song["NAME"].(string)
		name = strings.ReplaceAll(name, "&nbsp;", " ")

		artist, _ := song["ARTIST"].(string)

		album, _ := song["ALBUM"].(string)
		albumID := safeNumber(song["ALBUMID"])

		albumImgShort, _ := song["web_albumpic_short"].(string)
		albumImg := ""
		if albumImgShort != "" {
			albumImg = "https://img1.kuwo.cn/star/albumcover/" + albumImgShort
		}

		songs = append(songs, ScrapedSong{
			ID:       rid,
			Name:     name,
			Artist:   artist,
			Album:    album,
			AlbumID:  albumID,
			AlbumImg: albumImg,
			Provider: "kuwo",
		})
	}

	return songs, nil
}

func (p *KuwoProvider) FetchLyric(id string) (string, error) {
	return "", fmt.Errorf("kuwo lyric: requires login, lyrics not available")
}


