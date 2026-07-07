package bilibili

import (
	"context"
	"testing"
)

// TestResolveRealLive 对真实在播直播间取流。
// 用房间号 6（泛式，常驻预告/直播），验证取流链路通。
func TestResolveRealLive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real network test in short mode")
	}
	c := NewClient()
	ctx := context.Background()
	ls, err := c.Resolve(ctx, "6")
	if err != nil {
		if _, ok := err.(ErrNotLive); ok {
			t.Skip("room not live right now, skip")
		}
		t.Fatalf("Resolve: %v", err)
	}
	t.Logf("room=%s title=%q formats=%d", ls.RoomID, ls.Title, len(ls.Formats))
	t.Logf("referer=%s", ls.Referer)
	if len(ls.Formats) == 0 {
		t.Fatal("no formats")
	}
	for i, f := range ls.Formats {
		if i > 8 {
			t.Logf("  ...(%d more)", len(ls.Formats)-9)
			break
		}
		t.Logf("  qn=%d ext=%s vc=%s proto=%s url=%s", f.QN, f.Ext, f.VCodec, f.Protocol, trim(f.URL, 70))
	}
	// 校验格式字段合理
	f := ls.Formats[0]
	if f.URL == "" {
		t.Errorf("format missing url: %+v", f)
	}
	if ls.Referer != "https://live.bilibili.com/6" {
		t.Errorf("referer = %q, want https://live.bilibili.com/6", ls.Referer)
	}
	// 选优
	pick := PickPreferred(ls.Formats)
	t.Logf("picked: qn=%d ext=%s vc=%s proto=%s", pick.QN, pick.Ext, pick.VCodec, pick.Protocol)
}

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
