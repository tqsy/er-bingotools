package app

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"bingotools/internal/bilibili"
	"bingotools/internal/douyin"
	"bingotools/internal/proxy"
)

// douyinClient 是 douyin.Client 的别名。
type douyinClient = douyin.Client

func newDouyinClient() *douyinClient {
	return douyin.NewClient()
}

// App 是暴露给前端的方法接收器，并持有代理管理器。
type App struct {
	ctx     context.Context
	proxy   *proxy.Manager
	bili    *bilibili.Client
	dy      *douyinClient
}

func NewApp() *App {
	return &App{
		proxy: proxy.New(),
		bili:  bilibili.NewClient(),
		dy:    newDouyinClient(),
	}
}

func (a *App) OnStartup(ctx context.Context) {
	a.ctx = ctx
}

// StartupForTest 供无窗口测试注入 ctx。
func (a *App) StartupForTest(ctx context.Context) {
	a.ctx = ctx
}

// AssetsHandlerForTest 暴露代理 handler 供 main 与无窗口测试共用。
func (a *App) AssetsHandlerForTest() http.Handler {
	return a.assetsHandler()
}

// resolveResult 是返回给前端的取流结果。
type resolveResult struct {
	Kind  string `json:"kind"`  // "hls" | "flv"
	Title string `json:"title"`
	Room  string `json:"room"`
}

// ResolveLive 是前端调用的入口。
// source 形如 "bilibili:<roomId>" 或 "douyin:<web_rid|url>"。
// 取流后缓存到代理，前端用 /live/stream/<side> 播放。
func (a *App) ResolveLive(side int, source string) (resolveResult, error) {
	kind, title, room, info, err := a.resolveSource(source)
	if err != nil {
		return resolveResult{}, err
	}
	a.proxy.Set(side, info)
	return resolveResult{Kind: kind, Title: title, Room: room}, nil
}

func (a *App) resolveSource(source string) (string, string, string, proxy.StreamInfo, error) {
	parts := strings.SplitN(source, ":", 2)
	if len(parts) != 2 {
		return "", "", "", proxy.StreamInfo{}, fmt.Errorf("invalid source: %q", source)
	}
	platform, id := parts[0], parts[1]
	switch platform {
	case "bilibili":
		return a.resolveBilibili(id)
	case "douyin":
		return a.resolveDouyin(id)
	default:
		return "", "", "", proxy.StreamInfo{}, fmt.Errorf("unsupported platform: %q", platform)
	}
}

func (a *App) resolveBilibili(roomID string) (string, string, string, proxy.StreamInfo, error) {
	ls, err := a.bili.Resolve(a.ctx, roomID)
	if err != nil {
		return "", "", "", proxy.StreamInfo{}, err
	}
	pick := bilibili.PickPreferred(ls.Formats)
	kind := kindFromExt(pick.Ext, pick.Protocol)
	return kind, ls.Title, roomID, proxy.StreamInfo{
		URL:     pick.URL,
		BaseURL: baseURL(pick.URL),
		Referer: ls.Referer,
		UA:      ls.UA,
	}, nil
}

// baseURL 取 URL 的目录前缀（最后一个 / 之前）。
func baseURL(u string) string {
	idx := strings.LastIndex(u, "/")
	if idx < 0 {
		return ""
	}
	return u[:idx+1]
}

func (a *App) resolveDouyin(webRid string) (string, string, string, proxy.StreamInfo, error) {
	ls, err := a.dy.Resolve(a.ctx, webRid)
	if err != nil {
		return "", "", "", proxy.StreamInfo{}, err
	}
	pick := ls.BestFormat()
	kind := "hls"
	if pick.Kind == "flv" {
		kind = "flv"
	}
	return kind, ls.Title, ls.WebRid, proxy.StreamInfo{
		URL:     pick.URL,
		BaseURL: baseURL(pick.URL),
		Referer: "https://live.douyin.com/",
		Cookie:  ls.Cookie,
		UA:      ls.UA,
	}, nil
}

func kindFromExt(ext, proto string) string {
	switch {
	case ext == "ts", ext == "fmp4", proto == "http_hls", proto == "hls_ts":
		return "hls"
	case ext == "flv":
		return "flv"
	default:
		return "hls"
	}
}

// assetsHandler 兜底处理非静态资源请求：转发到代理。
func (a *App) assetsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/live/stream/") {
			a.proxy.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}
