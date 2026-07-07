package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParsePath(t *testing.T) {
	cases := []struct {
		path          string
		side          int
		segment       string
		ok            bool
	}{
		{"/live/stream/1", 1, "", true},
		{"/live/stream/2", 2, "", true},
		{"/live/stream/1/1783450826.m4s", 1, "1783450826.m4s", true},
		{"/live/stream/2/seg.ts", 2, "seg.ts", true},
		{"/live/stream/3", 0, "", false},      // side 越界
		{"/live/stream/", 0, "", false},      // 空
		{"/other/path", 0, "", false},        // 非代理路径
		{"/live/stream/1/", 1, "", true},      // 尾斜杠
	}
	for _, c := range cases {
		side, seg, ok := parsePath(c.path)
		if ok != c.ok || side != c.side || seg != c.segment {
			t.Errorf("parsePath(%q) = (%d,%q,%v), want (%d,%q,%v)",
				c.path, side, seg, ok, c.side, c.segment, c.ok)
		}
	}
}

func TestStreamInfoSegmentURL(t *testing.T) {
	s := StreamInfo{
		URL:     "https://cdn.example.com/media/stream-123.m3u8?token=abc",
		BaseURL: "https://cdn.example.com/media/",
	}
	got := s.segmentURL("456.ts")
	want := "https://cdn.example.com/media/456.ts"
	if got != want {
		t.Errorf("segmentURL = %q, want %q", got, want)
	}

	// BaseURL 为空时回退到 URL 推导
	s2 := StreamInfo{URL: "https://cdn.example.com/media/index.m3u8"}
	got2 := s2.segmentURL("seg.ts")
	want2 := "https://cdn.example.com/media/seg.ts"
	if got2 != want2 {
		t.Errorf("fallback segmentURL = %q, want %q", got2, want2)
	}
}

func TestSniffM3U8(t *testing.T) {
	if !isM3U8([]byte("#EXTM3U\n#EXT-X-VERSION:7")) {
		t.Error("isM3U8 should detect #EXTM3U")
	}
	if !isM3U8([]byte("  \n#EXTM3U\n")) {
		t.Error("isM3U8 should trim leading whitespace")
	}
	if isM3U8([]byte("FLV\x01")) {
		t.Error("isM3U8 should not match FLV")
	}
}

func TestServeHTTPRewritesM3U8(t *testing.T) {
	m := New()
	m.Set(1, StreamInfo{
		URL:     "https://cdn.example.com/live/index.m3u8",
		BaseURL: "https://cdn.example.com/live/",
	})

	// 模拟 CDN：返回相对路径的 m3u8
	cdnBody := "#EXTM3U\n#EXT-X-MAP:URI=\"init.m4s\"\n#EXTINF:1.00,\nseg001.m4s\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(cdnBody))
	}))
	defer server.Close()

	// 把缓存 URL 指向测试 server
	m.Set(1, StreamInfo{
		URL:     server.URL + "/index.m3u8",
		BaseURL: server.URL + "/",
	})

	req := httptest.NewRequest(http.MethodGet, "/live/stream/1", nil)
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "/live/stream/1/seg001.m4s") {
		t.Errorf("m3u8 not rewritten:\n%s", body)
	}
	if !strings.Contains(body, `URI="/live/stream/1/init.m4s"`) {
		t.Errorf("EXT-X-MAP URI not rewritten:\n%s", body)
	}
}

func TestRewriteM3U8(t *testing.T) {
	input := `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-MAP:URI="h1782477551.m4s"
#EXTINF:1.00,
1783453211.m4s
#EXTINF:1.00,
https://cdn.example.com/seg.ts
#EXTINF:1.00,
/abs/path/seg.ts
`
	got := string(rewriteM3U8([]byte(input), "/live/stream/1/"))
	want := `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-MAP:URI="/live/stream/1/h1782477551.m4s"
#EXTINF:1.00,
/live/stream/1/1783453211.m4s
#EXTINF:1.00,
https://cdn.example.com/seg.ts
#EXTINF:1.00,
/abs/path/seg.ts
`
	if got != want {
		t.Errorf("rewriteM3U8 mismatch:\n%s\n--- want ---\n%s", got, want)
	}
}

func TestSniffFlv(t *testing.T) {
	if !isFlv([]byte("FLV\x01\x05\x00\x00")) {
		t.Error("isFlv should detect FLV header")
	}
	if isFlv([]byte("#EXTM3U")) {
		t.Error("isFlv should not match m3u8")
	}
}
