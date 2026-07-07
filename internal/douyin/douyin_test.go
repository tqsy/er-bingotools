package douyin

import (
	"encoding/json"
	"testing"
)

func TestExtractWebRid(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"123456", "123456"},
		{"https://live.douyin.com/208823316033", "208823316033"},
		{"https://live.douyin.com/208823316033?from=foo", "208823316033"},
		{"https://live.douyin.com/abc123/", "abc123"},
		{"https://www.douyin.com/follow/live/577242340198", "577242340198"},
		{"https://www.douyin.com/user/abc?from_room_id=577242340198", "577242340198"},
		{"https://www.douyin.com/room/577242340198", "577242340198"},
		{"", ""},
		{"  999  ", "999"},
	}
	for _, c := range cases {
		got := extractWebRid(c.input)
		if got != c.want {
			t.Errorf("extractWebRid(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestParseStreamURLStreamOrientation2(t *testing.T) {
	// 构造 stream_orientation=2 的 pull_datas 分支
	su := streamURLData{
		StreamOrientation: 2,
		PullDatas: map[string]json.RawMessage{
			"1": json.RawMessage(`{"stream_data":"{\"FULL_HD1\":{\"main\":{\"flv\":\"http://flv.example\",\"hls\":\"http://hls.example\",\"sdk_params\":\"{\\\"vbitrate\\\":2000000}\"}}}","other":"x"}`),
		},
	}
	formats := parseStreamURL(su)
	if len(formats) != 2 {
		t.Fatalf("expected 2 formats, got %d", len(formats))
	}
	// 应含 hls 和 flv
	var hasHLS, hasFLV bool
	for _, f := range formats {
		switch f.Kind {
		case "hls":
			hasHLS = true
			if f.URL != "http://hls.example" {
				t.Errorf("hls url = %q", f.URL)
			}
		case "flv":
			hasFLV = true
			if f.URL != "http://flv.example" {
				t.Errorf("flv url = %q", f.URL)
			}
		}
	}
	if !hasHLS || !hasFLV {
		t.Errorf("missing kinds: hls=%v flv=%v", hasHLS, hasFLV)
	}
}

func TestParseStreamURLFallbackMaps(t *testing.T) {
	su := streamURLData{
		HlsPullURLMap: map[string]string{"HD1": "http://hls-hd.example"},
		FlvPullURLMap: map[string]string{"SD1": "http://flv-sd.example"},
	}
	formats := parseStreamURL(su)
	if len(formats) != 2 {
		t.Fatalf("expected 2 formats, got %d", len(formats))
	}
}
