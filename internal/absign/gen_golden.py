#!/usr/bin/env python3
"""生成 a_bogus 签名的黄金参照值，供 Go 移植对照。

关键：_douyin_generate_rc4_bb_str 内部用 time.time()，输出随时间变化。
本脚本 monkeypatch time.time 返回固定值，使输出确定可对照。
"""
import json
import sys
import os

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import douyin_sign_ref as ref

# 固定时间戳：start_time = int(time.time() * 1000)
FIXED_TIME = 1737000000.123  # → start_time_ms = 1737000000123
ref.time.time = lambda: FIXED_TIME
EXPECTED_START_MS = int(FIXED_TIME * 1000)

cases = {
    "fixed_time_ms": EXPECTED_START_MS,
    "rc4": [
        {"plaintext": "hello", "key": "y", "out": None},
        {"plaintext": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36", "key": "\x00\x01\x0e", "out": None},
        {"plaintext": "", "key": "y", "out": None},
        {"plaintext": "abc", "key": "\x00\x01\x0e", "out": None},
    ],
    "sm3_hex": [
        {"data": "", "out": None},
        {"data": "abc", "out": None},
        {"data": "abcdefghijklmnopqrstuvwxyz", "out": None},
        {"data": "aid=6383&app_name=douyin_web&web_rid=746299965912cus", "out": None},
        {"data": "cus", "out": None},
    ],
    "result_encrypt": [
        # 注意 long_str 是码点序列(0-255)，用 latin-1 解码构造
        {"long_str_hex": "0001080e", "num": "s3", "out": None},
        {"long_str_hex": "c8d4ff", "num": "s4", "out": None},
        {"long_str_hex": "414243", "num": "s0", "out": None},
    ],
    "generate_random_str": {"out": None},
    "generate_rc4_bb_str": [
        {"query": "aid=6383&app_name=douyin_web&live_id=1&device_platform=web&language=zh-CN&browser_language=zh-CN&browser_platform=Win32&browser_name=Chrome&browser_version=116.0.0.0&web_rid=746299965912&is_need_double_stream=false&msToken=",
         "user_agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
         "out": None},
        {"query": "aid=6383&web_rid=12345",
         "user_agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Safari/537.36",
         "out": None},
    ],
    "ab_sign": [
        {"query": "aid=6383&app_name=douyin_web&live_id=1&device_platform=web&language=zh-CN&browser_language=zh-CN&browser_platform=Win32&browser_name=Chrome&browser_version=116.0.0.0&web_rid=746299965912&is_need_double_stream=false&msToken=",
         "user_agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.0.0 Safari/537.36",
         "out": None},
        {"query": "aid=6383&web_rid=12345",
         "user_agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Safari/537.36",
         "out": None},
    ],
}

# rc4: 输出是码点序列(0-255)，转 hex 便于对照
for c in cases["rc4"]:
    out = ref._douyin_rc4_encrypt(c["plaintext"], c["key"])
    c["out"] = out.encode("latin-1").hex()

# sm3 hex
for c in cases["sm3_hex"]:
    sm3 = ref._DouyinSM3()
    c["out"] = sm3.sum(data=c["data"], output_format="hex")

# result_encrypt: long_str 从 hex 还原为码点序列 str
for c in cases["result_encrypt"]:
    raw = bytes.fromhex(c["long_str_hex"])
    long_str = "".join(chr(b) for b in raw)
    c["out"] = ref._douyin_result_encrypt(long_str, c["num"])
    # 同时保留 long_str 的 latin-1 表示以备 Go 对照
    c["long_str_latin1"] = long_str

# generate_random_str
cases["generate_random_str"]["out"] = cases["generate_random_str"]  # placeholder
rand_out = ref._douyin_generate_random_str()
cases["generate_random_str"] = {"out_hex": rand_out.encode("latin-1").hex()}

# generate_rc4_bb_str: 输出码点序列(0-255)，转 hex；同时导出中间值
def _gen_rc4_intermediates(query, user_agent, window_env_str):
    sm3 = ref._DouyinSM3()
    suffix = 'cus'
    url_search_params_list = sm3.sum(sm3.sum(query + suffix))
    cus = sm3.sum(sm3.sum(suffix))
    ua_key = chr(0) + chr(1) + chr(14)
    ua_enc = ref._douyin_rc4_encrypt(user_agent, ua_key)
    ua = sm3.sum(ref._douyin_result_encrypt(ua_enc, 's3'))
    return {
        'url_search_params_list_hex': bytes(url_search_params_list).hex(),
        'cus_hex': bytes(cus).hex(),
        'ua_enc_hex': ua_enc.encode('latin-1').hex(),
        'result_s3': ref._douyin_result_encrypt(ua_enc, 's3'),
        'ua_hex': bytes(ua).hex(),
        'out': ref._douyin_generate_rc4_bb_str(query, user_agent, window_env_str).encode('latin-1').hex(),
    }

for c in cases["generate_rc4_bb_str"]:
    inter = _gen_rc4_intermediates(c["query"], c["user_agent"],
                                  "1920|1080|1920|1040|0|30|0|0|1872|92|1920|1040|1857|92|1|24|Win32")
    c.update(inter)

# ab_sign: 输出 ASCII
for c in cases["ab_sign"]:
    c["out"] = ref._douyin_ab_sign(c["query"], c["user_agent"])

out_path = os.path.join(os.path.dirname(os.path.abspath(__file__)), "testdata", "golden.json")
with open(out_path, "w", encoding="utf-8") as f:
    json.dump(cases, f, ensure_ascii=False, indent=2)

print(f"written: {out_path}")
print(f"fixed_time_ms={EXPECTED_START_MS}")
print(f"ab_sign[0]={cases['ab_sign'][0]['out'][:60]}...")
