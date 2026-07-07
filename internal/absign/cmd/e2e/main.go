// e2e 端到端验证：用 Go 重写的 AbSign 实际请求抖音 enter API。
// 用法: go run ./cmd/e2e <web_rid>
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"

	"bingotools/internal/absign"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: e2e <web_rid>")
		os.Exit(1)
	}
	webRid := os.Args[1]

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// 1. 访问直播页拿 ttwid cookie
	liveURL := "https://live.douyin.com/" + webRid
	req, _ := http.NewRequest("GET", liveURL, nil)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://live.douyin.com/")
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fetch live page:", err)
		os.Exit(1)
	}
	resp.Body.Close()
	fmt.Println("ttwid:", jar.Cookies(&url.URL{Scheme: "https", Host: "live.douyin.com"}))

	// 2. 构造 query + a_bogus
	query := url.Values{}
	query.Set("aid", "6383")
	query.Set("app_name", "douyin_web")
	query.Set("live_id", "1")
	query.Set("device_platform", "web")
	query.Set("language", "zh-CN")
	query.Set("browser_language", "zh-CN")
	query.Set("browser_platform", "Win32")
	query.Set("browser_name", "Chrome")
	query.Set("browser_version", "116.0.0.0")
	query.Set("web_rid", webRid)
	query.Set("is_need_double_stream", "false")
	query.Set("msToken", "")
	queryStr := query.Encode()
	aBogus := absign.AbSign(queryStr, userAgent)
	apiURL := "https://live.douyin.com/webcast/room/web/enter/?" + queryStr + "&a_bogus=" + aBogus
	fmt.Println("a_bogus:", aBogus[:40]+"...")

	// 3. 请求 enter API
	req, _ = http.NewRequest("GET", apiURL, nil)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", "https://live.douyin.com/")
	resp, err = client.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "enter api:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Println("HTTP status:", resp.StatusCode)

	// 解析关键字段
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		fmt.Fprintln(os.Stderr, "json parse:", err)
		fmt.Println("body[:500]:", string(body[:min(500, len(body))]))
		os.Exit(1)
	}
	// 取 data.data[0].status 和 stream_url
	status := rawJSON(raw, "data", "data", 0, "status")
	title := rawJSON(raw, "data", "data", 0, "title")
	nickname := rawJSON(raw, "data", "user", "nickname")
	fmt.Println("status:", status)
	fmt.Println("title:", title)
	fmt.Println("nickname:", nickname)
	streamURL := rawJSON(raw, "data", "data", 0, "stream_url")
	if streamURL != nil {
		su, _ := streamURL.(map[string]any)
		fmt.Println("status_code:", rawJSON(raw, "status_code"))
		fmt.Println("orientation:", su["stream_orientation"])
		// hls_pull_url_map / flv_pull_url_map
		if hlsMap, ok := su["hls_pull_url_map"].(map[string]any); ok && len(hlsMap) > 0 {
			for q, u := range hlsMap {
				fmt.Printf("hls[%s]: %s\n", q, u)
				break
			}
		}
		if flvMap, ok := su["flv_pull_url_map"].(map[string]any); ok && len(flvMap) > 0 {
			for q, u := range flvMap {
				fmt.Printf("flv[%s]: %s\n", q, u)
				break
			}
		}
	} else {
		fmt.Println("stream_url: <none> (可能未开播或被风控)")
		// 打印顶层结构帮助诊断
		top, _ := json.MarshalIndent(raw, "", "  ")
		if len(top) > 800 {
			top = top[:800]
		}
		fmt.Println("raw:", string(top))
	}
}

func rawJSON(m map[string]any, path ...any) any {
	var cur any = m
	for _, p := range path {
		switch v := cur.(type) {
		case map[string]any:
			cur = v[fmt.Sprint(p)]
		case []any:
			idx, ok := p.(int)
			if !ok || idx < 0 || idx >= len(v) {
				return nil
			}
			cur = v[idx]
		default:
			return nil
		}
	}
	return cur
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
