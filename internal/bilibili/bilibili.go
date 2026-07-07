// Package bilibili 实现 B站直播流地址获取。
// 移植自 yt-dlp 的 BiliLiveIE（yt_dlp/extractor/bilibili.py），仅保留取流所需逻辑。
//
// 取流流程：
//  1. GET room/v1/Room/get_info?id=<roomId> → 校验 live_status（1=直播中）
//  2. GET xlive/web-room/v2/index/getRoomPlayInfo?... → playurl_info.playurl.stream[].format[].codec[]
//     过滤 codec.current_qn == qn，拼 url_info 的 host+base_url+extra
//  3. 播放注入 Referer: https://live.bilibili.com/<roomId>
package bilibili

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

const (
	apiBase    = "https://api.live.bilibili.com"
	livePageURL = "https://live.bilibili.com/"
	userAgent  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36"
)

// _FORMATS 对应 Python BiliLiveIE._FORMATS，qn → 格式信息。
// 实际返回的 current_qn 可能不在此表（如 qn=2），按 quality 数值越大越优。
var formatQN = []int{80, 150, 250, 400, 10000, 20000, 30000}

// Format 表示一个可播放的流格式。
type Format struct {
	URL      string `json:"url"`
	Ext      string `json:"ext"`      // flv / fmp4 / ts
	VCodec   string `json:"vcodec"`   // avc / hevc
	QN       int    `json:"qn"`       // 画质代号
	Quality  int    `json:"quality"`  // 画质数值（越大越优）
	Protocol string `json:"protocol"` // http_stream / hls_ts
}

// LiveStream 是取流结果。
type LiveStream struct {
	RoomID  string    `json:"room_id"`
	Title   string    `json:"title"`
	Formats []Format  `json:"formats"`
	Referer string    `json:"referer"` // 注入到播放请求
	UA      string    `json:"ua"`
}

type roomInfo struct {
	Code int             `json:"code"`
	Msg  string          `json:"message"`
	Data json.RawMessage `json:"data"`
}

// Client 封装 B站直播取流。
type Client struct {
	HTTP *http.Client
}

func NewClient() *Client {
	return &Client{HTTP: &http.Client{}}
}

func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	u := apiBase + "/" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	var info roomInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if info.Code != 0 {
		return fmt.Errorf("%s: code=%d %s", path, info.Code, info.Msg)
	}
	if len(info.Data) == 0 {
		return nil
	}
	return json.Unmarshal(info.Data, out)
}

type getRoomInfoData struct {
	LiveStatus int    `json:"live_status"`
	Title      string `json:"title"`
}

type getRoomPlayInfoData struct {
	PlayurlInfo struct {
		Playurl struct {
			Stream []struct {
				ProtocolName string `json:"protocol_name"`
				Format       []struct {
					FormatName string `json:"format_name"`
					Codec      []struct {
						CurrentQN int    `json:"current_qn"`
						CodecName string `json:"codec_name"`
						BaseURL   string `json:"base_url"`
						URLInfo   []struct {
							Host  string `json:"host"`
							Extra string `json:"extra"`
						} `json:"url_info"`
					} `json:"codec"`
				} `json:"format"`
			} `json:"stream"`
		} `json:"playurl"`
	} `json:"playurl_info"`
}

// Resolve 获取指定直播间的所有流格式。
// 遍历所有 qn 请求，合并去重，返回按画质降序排列的格式列表。
func (c *Client) Resolve(ctx context.Context, roomID string) (*LiveStream, error) {
	// 1. 校验直播状态
	var info getRoomInfoData
	q := url.Values{"id": {roomID}}
	if err := c.getJSON(ctx, "room/v1/Room/get_info", q, &info); err != nil {
		return nil, fmt.Errorf("get_info: %w", err)
	}
	if info.LiveStatus == 0 {
		return nil, ErrNotLive{RoomID: roomID}
	}

	// 2. 逐 qn 取流
	var allFormats []Format
	seen := map[string]bool{}
	for _, qn := range formatQN {
		var playInfo getRoomPlayInfoData
		q := url.Values{
			"room_id":    {roomID},
			"qn":         {strconv.Itoa(qn)},
			"codec":      {"0,1"},
			"format":     {"0,2"},
			"mask":       {"0"},
			"no_playurl": {"0"},
			"platform":   {"web"},
			"protocol":   {"0,1"},
		}
		if err := c.getJSON(ctx, "xlive/web-room/v2/index/getRoomPlayInfo", q, &playInfo); err != nil {
			// 某些 qn 可能不可用，跳过
			continue
		}
		for _, stream := range playInfo.PlayurlInfo.Playurl.Stream {
			for _, fmt := range stream.Format {
				for _, codec := range fmt.Codec {
					if codec.CurrentQN != qn {
						continue
					}
					for _, ui := range codec.URLInfo {
						streamURL := ui.Host + codec.BaseURL + ui.Extra
						if seen[streamURL] {
							continue
						}
						seen[streamURL] = true
						allFormats = append(allFormats, Format{
							URL:      streamURL,
							Ext:      fmt.FormatName,
							VCodec:   codec.CodecName,
							QN:       qn,
							Quality:  qn,
							Protocol: stream.ProtocolName,
						})
					}
				}
			}
		}
	}

	if len(allFormats) == 0 {
		return nil, fmt.Errorf("no stream formats found for room %s", roomID)
	}

	// 按画质降序（qn 越大越优）
	sortFormats(allFormats)

	return &LiveStream{
		RoomID:  roomID,
		Title:   info.Title,
		Formats: allFormats,
		Referer: livePageURL + roomID,
		UA:      userAgent,
	}, nil
}

// PickPreferred 从格式列表中选择推荐播放格式。
// 优先 HLS (hls_ts/ts) 便于 seek，其次 fmp4，最后 flv。
// 视频编码优先 avc（兼容性最好），hevc 兜底。
func PickPreferred(formats []Format) Format {
	// 优先级: protocol hls_ts > ext fmp4 > ext flv; vcodec avc 优先
	score := func(f Format) int {
		s := 0
		switch {
		case f.Protocol == "http_hls" || f.Protocol == "hls_ts" || f.Ext == "ts" || f.Ext == "fmp4":
			s += 100
		case f.Ext == "flv":
			s += 10
		}
		if f.VCodec == "avc" || f.VCodec == "h264" {
			s += 5
		}
		s += f.Quality // 画质加权
		return s
	}
	best := formats[0]
	bestScore := score(best)
	for _, f := range formats[1:] {
		if s := score(f); s > bestScore {
			best, bestScore = f, s
		}
	}
	return best
}

func sortFormats(f []Format) {
	// 简单插入排序，按 Quality 降序（数据量小）
	for i := 1; i < len(f); i++ {
		for j := i; j > 0 && f[j].Quality > f[j-1].Quality; j-- {
			f[j], f[j-1] = f[j-1], f[j]
		}
	}
}

// ErrNotLive 直播间未开播。
type ErrNotLive struct{ RoomID string }

func (e ErrNotLive) Error() string { return "streamer is not live: " + e.RoomID }
