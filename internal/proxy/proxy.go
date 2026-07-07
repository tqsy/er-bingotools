// Package proxy 实现同源反向代理，规避 CORS。
// 前端 <video> 访问 /live/stream/<side>，本代理取缓存的真实流地址，
// 注入 Referer/Cookie/UA 后流式转发 CDN 响应。
package proxy

import (
	"bytes"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// StreamInfo 缓存的取流结果。
type StreamInfo struct {
	URL     string // m3u8/flv 主流地址
	BaseURL string // m3u8 所在目录（用于分片转发）；为空时回退到 URL
	Referer string
	Cookie  string
	UA      string
}

// segmentURL 根据 side 和 segment 构造 CDN 分片地址。
// side 主流 URL 是 m3u8，BaseURL 记录其目录前缀。
func (s *StreamInfo) segmentURL(segment string) string {
	if s.BaseURL != "" {
		return s.BaseURL + segment
	}
	// 回退：从 URL 推导目录
	idx := strings.LastIndex(s.URL, "/")
	if idx < 0 {
		return s.URL
	}
	return s.URL[:idx+1] + segment
}

// Manager 管理各直播位的取流结果并处理代理请求。
type Manager struct {
	mu       sync.RWMutex
	streams  map[int]*StreamInfo // side(1|2) -> info
	client   *http.Client
}

func New() *Manager {
	return &Manager{
		streams: make(map[int]*StreamInfo),
		client:  &http.Client{},
	}
}

// Set 缓存指定位的流信息。
func (m *Manager) Set(side int, info StreamInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streams[side] = &info
}

// Get 取指定位的流信息。
func (m *Manager) Get(side int) *StreamInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.streams[side]
}

// Clear 清除指定位的流信息。
func (m *Manager) Clear(side int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.streams, side)
}

// ServeHTTP 处理 /live/stream/<side> 请求，反向代理到真实流地址。
// 透传 Range 请求支持 seek，流式 io.Copy 转发响应体。
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CORS 保险（同源本不需要）
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Range")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	// 解析 /live/stream/<side>[/<segment>]
	side, segment, ok := parsePath(r.URL.Path)
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	info := m.Get(side)
	if info == nil {
		http.Error(w, "stream not resolved", http.StatusNotFound)
		return
	}

	// 主流或分片 URL
	upstream := info.URL
	if segment != "" {
		upstream = info.segmentURL(segment)
	}

	// 构造到 CDN 的请求
	upReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstream, nil)
	if err != nil {
		http.Error(w, "bad upstream: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// 注入头
	if info.UA != "" {
		upReq.Header.Set("User-Agent", info.UA)
	}
	if info.Referer != "" {
		upReq.Header.Set("Referer", info.Referer)
	}
	if info.Cookie != "" {
		upReq.Header.Set("Cookie", info.Cookie)
	}
	// 透传 Range（seek）
	if rng := r.Header.Get("Range"); rng != "" {
		upReq.Header.Set("Range", rng)
	}

	resp, err := m.client.Do(upReq)
	if err != nil {
		http.Error(w, "upstream: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 先读取首块以嗅探内容类型（m3u8/flv），再决定 Content-Type 并写状态码
	firstBuf := make([]byte, 4096)
	n, _ := resp.Body.Read(firstBuf)

	// 若是主流 m3u8，读取完整并重写相对分片路径，避免前端请求 URL 末尾无斜杠导致分片 404
	if segment == "" && isM3U8(firstBuf[:n]) {
		rest, _ := io.ReadAll(resp.Body)
		full := append(firstBuf[:n], rest...)
		prefix := "/live/stream/" + strconv.Itoa(side) + "/"
		rewritten := rewriteM3U8(full, prefix)
		w.Header().Set("Content-Length", strconv.Itoa(len(rewritten)))
		copyHeaders(w.Header(), resp.Header, "Content-Range", "Accept-Ranges", "Cache-Control")
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(rewritten)
		return
	}

	// 透传关键响应头（先 copy，再修正 Content-Type）
	copyHeaders(w.Header(), resp.Header, "Content-Length",
		"Content-Range", "Accept-Ranges", "Cache-Control")
	ct := resp.Header.Get("Content-Type")
	if isM3U8(firstBuf[:n]) || strings.Contains(info.URL, ".m3u8") {
		if !strings.Contains(ct, "mpegurl") {
			ct = "application/vnd.apple.mpegurl"
		}
	} else if isFlv(firstBuf[:n]) {
		if !strings.Contains(ct, "flv") {
			ct = "video/x-flv"
		}
	}
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}

	w.WriteHeader(resp.StatusCode)

	// 先写已读的首块，再继续流式转发
	_, _ = w.Write(firstBuf[:n])
	_, _ = io.Copy(w, resp.Body)
}

// parsePath 解析 /live/stream/<side>[/<segment>]，返回 side 与 segment（可能为空）。
func parsePath(path string) (int, string, bool) {
	if !strings.HasPrefix(path, "/live/stream/") {
		return 0, "", false
	}
	tail := strings.TrimPrefix(path, "/live/stream/")
	tail = strings.TrimPrefix(tail, "/")
	if tail == "" {
		return 0, "", false
	}
	// side 是首段（1/2），其余为 segment
	sideStr := tail
	segment := ""
	if idx := strings.IndexByte(tail, '/'); idx >= 0 {
		sideStr = tail[:idx]
		segment = strings.TrimPrefix(tail[idx:], "/")
	}
	if len(sideStr) != 1 || sideStr[0] < '1' || sideStr[0] > '2' {
		return 0, "", false
	}
	return int(sideStr[0] - '0'), segment, true
}

// uriAttrRE 匹配 HLS 标签中的 URI="..." 属性。
var uriAttrRE = regexp.MustCompile(`URI="([^"]+)"`)

// rewriteM3U8 把 m3u8 中的相对路径重写为以 prefix 开头的绝对路径。
func rewriteM3U8(body []byte, prefix string) []byte {
	var out bytes.Buffer
	lines := strings.Split(string(body), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			// 对含 URI= 的 tag 仍要重写内部路径
			if uriAttrRE.MatchString(line) {
				line = uriAttrRE.ReplaceAllStringFunc(line, func(m string) string {
					uri := uriAttrRE.FindStringSubmatch(m)[1]
					return `URI="` + rewriteSegment(uri, prefix) + `"`
				})
			}
			out.WriteString(line)
		} else {
			out.WriteString(rewriteSegment(trimmed, prefix))
		}
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return out.Bytes()
}

func rewriteSegment(s, prefix string) string {
	// 已是绝对 URL 或绝对路径则不处理
	if strings.Contains(s, "://") || strings.HasPrefix(s, "/") {
		return s
	}
	return prefix + s
}

func copyHeaders(dst, src http.Header, names ...string) {
	for _, n := range names {
		if v := src.Get(n); v != "" {
			dst.Set(n, v)
		}
	}
}

// isM3U8 嗅探 HLS 播放列表。
func isM3U8(b []byte) bool {
	s := string(b)
	return strings.HasPrefix(strings.TrimSpace(s), "#EXTM3U")
}

// isFlv 嗅探 FLV 文件头 (FLV\x01)。
func isFlv(b []byte) bool {
	return len(b) >= 3 && b[0] == 'F' && b[1] == 'L' && b[2] == 'V'
}
