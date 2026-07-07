// Package douyin 实现抖音直播流地址获取，复用 absign 签名。
// 移植自 yt-dlp fork 的 DouyinLiveIE。
package douyin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"

	"bingotools/internal/absign"
)

const (
	livePageHost = "live.douyin.com"
	userAgent    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36"
	windowEnvStr = "1920|1080|1920|1040|0|30|0|0|1872|92|1920|1040|1857|92|1|24|Win32"
)

// Format 表示一个抖音直播流格式。
type Format struct {
	URL    string
	Kind   string // "hls" | "flv"
	Quality string
	Bitrate int
}

// LiveStream 是取流结果。
type LiveStream struct {
	WebRid string
	Title  string
	Formats []Format
	Cookie  string // ttwid 等
	UA      string
}

func (ls *LiveStream) BestFormat() Format {
	// 优先 HLS，其次 FLV；bitrate 越大越优
	best := Format{}
	if len(ls.Formats) == 0 {
		return best
	}
	best = ls.Formats[0]
	score := func(f Format) int {
		s := 0
		if f.Kind == "hls" {
			s += 1000
		}
		s += f.Bitrate
		return s
	}
	bestScore := score(best)
	for _, f := range ls.Formats[1:] {
		if s := score(f); s > bestScore {
			best, bestScore = f, s
		}
	}
	return best
}

// Client 封装抖音取流。
type Client struct {
	HTTP *http.Client
}

func NewClient() *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{HTTP: &http.Client{Jar: jar}}
}

func (c *Client) get(ctx context.Context, u, referer string) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return resp, body, err
}

// extractWebRid 从输入解析 web_rid。
// 支持纯数字、live.douyin.com/<rid>、www.douyin.com/follow/live/<rid> 等抖音直播 URL。
func extractWebRid(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}

	looksLikeRid := func(s string) bool {
		// 抖音 web_rid 通常为纯数字；也允许数字+字母组合
		return s != "" && (strings.TrimFunc(s, func(r rune) bool {
			return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
		}) == "")
	}

	if strings.HasPrefix(input, "http") {
		u, err := url.Parse(input)
		if err == nil {
			q := u.Query()
			// 优先 query 中的 room_id / from_room_id
			for _, key := range []string{"room_id", "from_room_id"} {
				if v := q.Get(key); v != "" && looksLikeRid(v) {
					return v
				}
			}

			// 取 path 最后一段（basename）作为 rid
			seg := strings.Trim(u.Path, "/")
			if idx := strings.LastIndexByte(seg, '/'); idx >= 0 {
				seg = seg[idx+1:]
			}
			if looksLikeRid(seg) {
				return seg
			}
		}
	}

	// 纯数字/字母直接返回
	if looksLikeRid(input) {
		return input
	}
	return ""
}

// Resolve 获取指定抖音直播间的所有流格式。
func (c *Client) Resolve(ctx context.Context, webRidOrURL string) (*LiveStream, error) {
	webRid := extractWebRid(webRidOrURL)
	if webRid == "" {
		return nil, fmt.Errorf("invalid douyin source: %q", webRidOrURL)
	}
	referer := "https://" + livePageHost + "/"

	// 1. 访问直播页拿 ttwid cookie
	if _, _, err := c.get(ctx, "https://"+livePageHost+"/"+webRid, referer); err != nil {
		return nil, fmt.Errorf("fetch live page: %w", err)
	}

	// 2. 构造 query + a_bogus
	q := url.Values{}
	q.Set("aid", "6383")
	q.Set("app_name", "douyin_web")
	q.Set("live_id", "1")
	q.Set("device_platform", "web")
	q.Set("language", "zh-CN")
	q.Set("browser_language", "zh-CN")
	q.Set("browser_platform", "Win32")
	q.Set("browser_name", "Chrome")
	q.Set("browser_version", "116.0.0.0")
	q.Set("web_rid", webRid)
	q.Set("is_need_double_stream", "false")
	q.Set("msToken", "")
	queryStr := q.Encode()
	aBogus := absign.AbSign(queryStr, userAgent)
	apiURL := "https://" + livePageHost + "/webcast/room/web/enter/?" + queryStr + "&a_bogus=" + aBogus

	// 3. 请求 enter API
	_, body, err := c.get(ctx, apiURL, referer)
	if err != nil {
		return nil, fmt.Errorf("enter api: %w", err)
	}
	var raw enterResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse enter: %w", err)
	}
	if raw.StatusCode != 0 {
		return nil, fmt.Errorf("douyin api error: status_code=%d", raw.StatusCode)
	}
	if len(raw.Data.Data) == 0 {
		return nil, fmt.Errorf("no room data")
	}
	room := raw.Data.Data[0]
	if room.Status == 4 {
		return nil, fmt.Errorf("douyin streamer not live: %s", webRid)
	}

	// 4. 解析 stream_url
	formats := parseStreamURL(room.StreamURL)
	if len(formats) == 0 {
		return nil, fmt.Errorf("no stream formats found for %s", webRid)
	}

	// 收集 cookie 串
	cookies := c.HTTP.Jar.Cookies(&url.URL{Scheme: "https", Host: livePageHost})
	var cookieParts []string
	for _, ck := range cookies {
		cookieParts = append(cookieParts, ck.Name+"="+ck.Value)
	}

	return &LiveStream{
		WebRid:  webRid,
		Title:   room.Title,
		Formats: formats,
		Cookie:  strings.Join(cookieParts, "; "),
		UA:      userAgent,
	}, nil
}

type enterResponse struct {
	StatusCode int   `json:"status_code"`
	Data       struct {
		Data []roomData `json:"data"`
		User struct {
			Nickname string `json:"nickname"`
		} `json:"user"`
	} `json:"data"`
}

type roomData struct {
	Status    int            `json:"status"`
	Title     string         `json:"title"`
	StreamURL streamURLData  `json:"stream_url"`
}

type streamURLData struct {
	StreamOrientation int            `json:"stream_orientation"`
	PullDatas         map[string]json.RawMessage `json:"pull_datas"`
	LiveCoreSdkData   struct {
		PullData struct {
			StreamData string `json:"stream_data"`
		} `json:"pull_data"`
	} `json:"live_core_sdk_data"`
	HlsPullURLMap map[string]string `json:"hls_pull_url_map"`
	FlvPullURLMap map[string]string `json:"flv_pull_url_map"`
}

func parseStreamURL(su streamURLData) []Format {
	var formats []Format
	parseInner := func(s string) map[string]any {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			return nil
		}
		return m
	}

	if su.StreamOrientation == 2 && len(su.PullDatas) > 0 {
		// pull_datas 分支
		for _, raw := range su.PullDatas {
			var pd struct {
				StreamData string `json:"stream_data"`
			}
			if json.Unmarshal(raw, &pd) != nil {
				continue
			}
			data := parseInner(pd.StreamData)
			if data == nil {
				continue
			}
			for quality, sv := range data {
				s, ok := sv.(map[string]any)
				if !ok {
					continue
				}
				main, _ := s["main"].(map[string]any)
				if main == nil {
					continue
				}
				sdk := parseInner(getString(main, "sdk_params"))
				bitrate := getInt(sdk, "vbitrate")
				if hls := getString(main, "hls"); hls != "" {
					formats = append(formats, Format{URL: hls, Kind: "hls", Quality: quality, Bitrate: bitrate})
				}
				if flv := getString(main, "flv"); flv != "" {
					formats = append(formats, Format{URL: flv, Kind: "flv", Quality: quality, Bitrate: bitrate})
				}
			}
		}
	} else {
		// live_core_sdk_data 分支
		data := parseInner(su.LiveCoreSdkData.PullData.StreamData)
		for quality, sv := range data {
			s, ok := sv.(map[string]any)
			if !ok {
				continue
			}
			main, _ := s["main"].(map[string]any)
			if main == nil {
				continue
			}
			sdk := parseInner(getString(main, "sdk_params"))
			bitrate := getInt(sdk, "vbitrate")
			if hls := getString(main, "hls"); hls != "" {
				formats = append(formats, Format{URL: hls, Kind: "hls", Quality: quality, Bitrate: bitrate})
			}
			if flv := getString(main, "flv"); flv != "" {
				formats = append(formats, Format{URL: flv, Kind: "flv", Quality: quality, Bitrate: bitrate})
			}
		}
	}

	// 兜底：hls_pull_url_map / flv_pull_url_map
	existing := map[string]bool{}
	for _, f := range formats {
		existing[f.URL] = true
	}
	for q, u := range su.HlsPullURLMap {
		if u != "" && !existing[u] {
			formats = append(formats, Format{URL: u, Kind: "hls", Quality: q})
			existing[u] = true
		}
	}
	for q, u := range su.FlvPullURLMap {
		if u != "" && !existing[u] {
			formats = append(formats, Format{URL: u, Kind: "flv", Quality: q})
			existing[u] = true
		}
	}
	return formats
}

func getString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key].(string)
	if !ok {
		return ""
	}
	return v
}

func getInt(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case string:
		var n int
		fmt.Sscanf(v, "%d", &n)
		return n
	}
	return 0
}
