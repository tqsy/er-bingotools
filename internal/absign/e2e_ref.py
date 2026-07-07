#!/usr/bin/env python3
"""Python e2e 对照：与 Go e2e 完全相同流程，对比响应。"""
import sys
import json
import urllib.request
import urllib.parse
import http.cookiejar

sys.path.insert(0, ".")
import douyin_sign_ref as ref

UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36"
web_rid = sys.argv[1] if len(sys.argv) > 1 else "746299965912"

jar = http.cookiejar.CookieJar()
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))

# 1. 访问直播页拿 ttwid
req = urllib.request.Request(f"https://live.douyin.com/{web_rid}", headers={"User-Agent": UA, "Referer": "https://live.douyin.com/"})
opener.open(req).read()
print("ttwid:", [c.name for c in jar])

# 2. 构造 query + a_bogus
query = {
    "aid": "6383", "app_name": "douyin_web", "live_id": "1", "device_platform": "web",
    "language": "zh-CN", "browser_language": "zh-CN", "browser_platform": "Win32",
    "browser_name": "Chrome", "browser_version": "116.0.0.0",
    "web_rid": web_rid, "is_need_double_stream": "false", "msToken": "",
}
query_str = urllib.parse.urlencode(query)
a_bogus = ref._douyin_ab_sign(query_str, UA)
api = f"https://live.douyin.com/webcast/room/web/enter/?{query_str}&a_bogus={a_bogus}"
print("a_bogus:", a_bogus[:40] + "...")

# 3. 请求 enter API
req = urllib.request.Request(api, headers={"User-Agent": UA, "Referer": "https://live.douyin.com/"})
body = opener.open(req).read()
raw = json.loads(body)
print("status_code:", raw.get("status_code"))
print("prompts:", raw.get("data", {}).get("prompts"))
data0 = raw.get("data", {}).get("data", [{}])[0] if raw.get("data", {}).get("data") else {}
print("room status:", data0.get("status"))
print("title:", data0.get("title"))
if data0.get("stream_url"):
    su = data0["stream_url"]
    print("stream_url keys:", list(su.keys())[:6])
    # 找 flv/hls
    lcd = su.get("live_core_sdk_data", {})
    if lcd:
        print("  has live_core_sdk_data")
else:
    print("stream_url: <none>")
