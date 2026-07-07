// 无窗口端到端验证：用 net/http 启动 App 的代理逻辑，
// 模拟前端调 ResolveLive 后用 curl/ffmpeg 访问 /live/stream/<side>。
// go run ./cmd/headless <platform> <id>
//   例: go run ./cmd/headless bilibili 6
//       go run ./cmd/headless douyin 208823316033
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"bingotools/internal/app"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: headless <bilibili|douyin> <id>")
		os.Exit(1)
	}
	platform, id := os.Args[1], os.Args[2]
	source := platform + ":" + id

	appObj := app.NewApp()
	ctx := context.Background()
	appObj.StartupForTest(ctx)

	res, err := appObj.ResolveLive(1, source)
	if err != nil {
		fmt.Println("ResolveLive error:", err)
		os.Exit(1)
	}
	fmt.Printf("resolved: kind=%s title=%q room=%s\n", res.Kind, res.Title, res.Room)

	// 启动 http server 暴露代理
	mux := http.NewServeMux()
	mux.Handle("/live/", appObj.AssetsHandlerForTest())
	srv := &http.Server{Addr: "127.0.0.1:18099", Handler: mux}
	go func() { _ = srv.ListenAndServe() }()
	time.Sleep(200 * time.Millisecond)
	proxyURL := "http://127.0.0.1:18099/live/stream/1"
	fmt.Println("proxy url:", proxyURL)

	// HTTP 探测
	resp, err := http.Get(proxyURL)
	if err != nil {
		fmt.Println("proxy GET error:", err)
		os.Exit(1)
	}
	ct := resp.Header.Get("Content-Type")
	fmt.Printf("proxy HTTP %d, content-type=%s, len=%d\n", resp.StatusCode, ct, resp.ContentLength)
	buf := make([]byte, 8192)
	n, _ := resp.Body.Read(buf)
	fmt.Printf("first %d bytes (m3u8 content):\n%s\n", n, string(buf[:n]))
	resp.Body.Close()
	_ = io.Discard

	// 探测分片转发：从 m3u8 提取真实分片名
	segName := extractFirstSegment(buf[:n])
	if segName != "" {
		segURL := proxyURL + "/" + segName
		segResp, err := http.Get(segURL)
		if err != nil {
			fmt.Println("segment probe error:", err)
		} else {
			fmt.Printf("segment %q probe: HTTP %d content-type=%s len=%d\n", segName, segResp.StatusCode, segResp.Header.Get("Content-Type"), segResp.ContentLength)
			segResp.Body.Close()
		}
	}

	// ffmpeg 探测
	fmt.Println("=== ffmpeg probe (5s) ===")
	cmd := exec.Command("ffmpeg", "-i", proxyURL, "-t", "5", "-f", "null", "-")
	out, _ := cmd.CombinedOutput()
	s := string(out)
	if s == "" {
		fmt.Println("(ffmpeg 无输出)")
	}
	for _, kw := range []string{"Input #", "Stream #0:0", "Stream #0:1", "Error", "404", "Invalid"} {
		if i := indexStr(s, kw); i >= 0 {
			end := i
			for end < len(s) && s[end] != '\n' {
				end++
			}
			fmt.Println(s[i:end])
		}
	}
	_ = srv.Close()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func indexStr(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func extractFirstSegment(m3u8 []byte) string {
	for _, line := range strings.Split(string(m3u8), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}
