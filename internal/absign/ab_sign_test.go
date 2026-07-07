package absign

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type goldenFile struct {
	FixedTimeMs int64 `json:"fixed_time_ms"`
	RC4         []struct {
		Plaintext string `json:"plaintext"`
		Key       string `json:"key"`
		Out       string `json:"out"` // hex
	} `json:"rc4"`
	SM3Hex []struct {
		Data string `json:"data"`
		Out  string `json:"out"`
	} `json:"sm3_hex"`
	ResultEncrypt []struct {
		LongStrHex string `json:"long_str_hex"`
		Num        string `json:"num"`
		Out        string `json:"out"`
	} `json:"result_encrypt"`
	GenerateRandomStr struct {
		OutHex string `json:"out_hex"`
	} `json:"generate_random_str"`
	GenerateRc4BbStr []struct {
		Query                       string `json:"query"`
		UserAgent                   string `json:"user_agent"`
		Out                         string `json:"out"`
		URLSearchParamsListHex      string `json:"url_search_params_list_hex"`
		CusHex                      string `json:"cus_hex"`
		UAEncHex                    string `json:"ua_enc_hex"`
		ResultS3                    string `json:"result_s3"`
		UAHex                       string `json:"ua_hex"`
	} `json:"generate_rc4_bb_str"`
	AbSign []struct {
		Query     string `json:"query"`
		UserAgent string `json:"user_agent"`
		Out       string `json:"out"`
	} `json:"ab_sign"`
}

func loadGolden(t *testing.T) *goldenFile {
	t.Helper()
	path := filepath.Join("testdata", "golden.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	var g goldenFile
	if err := json.Unmarshal(data, &g); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	return &g
}

// latin1ToRunes 把 hex 字节还原为码点序列（每字节一个 rune），对应 Python chr(b)。
func latin1ToRunes(hexStr string) []rune {
	b, _ := hex.DecodeString(hexStr)
	r := make([]rune, len(b))
	for i, by := range b {
		r[i] = rune(by)
	}
	return r
}

func TestRC4(t *testing.T) {
	g := loadGolden(t)
	for i, c := range g.RC4 {
		// plaintext/key 在 Python 中是 str，作为码点序列处理。
		// 测试用例均为 ASCII，[]rune(string) 等价 ord()。
		got := rc4Encrypt([]rune(c.Plaintext), []rune(c.Key))
		// 码点序列 → latin-1 字节 → hex
		gotBytes := make([]byte, len(got))
		for j, r := range got {
			gotBytes[j] = byte(r)
		}
		want, _ := hex.DecodeString(c.Out)
		if !bytes.Equal(gotBytes, want) {
			t.Errorf("rc4[%d]: got %x want %x", i, gotBytes, want)
		}
	}
}

func TestSM3(t *testing.T) {
	g := loadGolden(t)
	for i, c := range g.SM3Hex {
		sm3 := NewSM3()
		got := sm3.SumHex([]byte(c.Data))
		if got != c.Out {
			t.Errorf("sm3[%d] %q: got %s want %s", i, c.Data, got, c.Out)
		}
	}
}

func TestSM3StandardVector(t *testing.T) {
	// 标准 SM3 测试向量：SM3("abc")
	sm3 := NewSM3()
	got := sm3.SumHex([]byte("abc"))
	const want = "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0"
	if got != want {
		t.Errorf("sm3(abc): got %s want %s", got, want)
	}
}

func TestResultEncrypt(t *testing.T) {
	g := loadGolden(t)
	for i, c := range g.ResultEncrypt {
		longStr := latin1ToRunes(c.LongStrHex)
		got := resultEncrypt(longStr, c.Num)
		if got != c.Out {
			t.Errorf("result_encrypt[%d]: got %q want %q", i, got, c.Out)
		}
	}
}

func TestGenerateRandomStr(t *testing.T) {
	g := loadGolden(t)
	got := generateRandomStr()
	gotBytes := make([]byte, len(got))
	for i, r := range got {
		gotBytes[i] = byte(r)
	}
	want, _ := hex.DecodeString(g.GenerateRandomStr.OutHex)
	if !bytes.Equal(gotBytes, want) {
		t.Errorf("generate_random_str: got %x want %x", gotBytes, want)
	}
}

func TestRc4BbIntermediates(t *testing.T) {
	g := loadGolden(t)
	for i, c := range g.GenerateRc4BbStr {
		// urlSearchParamsList = sm3.sum(sm3.sum(query+"cus"))
		sm3 := NewSM3()
		urlList := sm3.Sum(sm3.Sum([]byte(c.Query + "cus")))
		wantURL, _ := hex.DecodeString(c.URLSearchParamsListHex)
		if !bytes.Equal(urlList, wantURL) {
			t.Errorf("case[%d] url_search_params_list: got %x want %x", i, urlList, wantURL)
		}
		// cus = sm3.sum(sm3.sum("cus"))
		cus := sm3.Sum(sm3.Sum([]byte("cus")))
		wantCus, _ := hex.DecodeString(c.CusHex)
		if !bytes.Equal(cus, wantCus) {
			t.Errorf("case[%d] cus: got %x want %x", i, cus, wantCus)
		}
		// ua_enc = rc4(userAgent, [0,1,14])
		uaEnc := rc4Encrypt([]rune(c.UserAgent), []rune{0, 1, 14})
		uaEncBytes := make([]byte, len(uaEnc))
		for j, r := range uaEnc {
			uaEncBytes[j] = byte(r)
		}
		wantUAEnc, _ := hex.DecodeString(c.UAEncHex)
		if !bytes.Equal(uaEncBytes, wantUAEnc) {
			t.Errorf("case[%d] ua_enc: got %x want %x", i, uaEncBytes, wantUAEnc)
		}
		// ua = sm3.sum(resultEncrypt(uaEnc, "s3"))
		resS3 := resultEncrypt(uaEnc, "s3")
		if resS3 != c.ResultS3 {
			t.Errorf("case[%d] result_s3:\n got %q\nwant %q", i, resS3, c.ResultS3)
		}
		ua := sm3.Sum([]byte(resS3))
		wantUA, _ := hex.DecodeString(c.UAHex)
		if !bytes.Equal(ua, wantUA) {
			t.Errorf("case[%d] ua: got %x want %x", i, ua, wantUA)
		}
	}
}

func TestGenerateRc4BbStr(t *testing.T) {
	g := loadGolden(t)
	windowEnv := "1920|1080|1920|1040|0|30|0|0|1872|92|1920|1040|1857|92|1|24|Win32"
	for i, c := range g.GenerateRc4BbStr {
		got := generateRc4BbStr(c.Query, c.UserAgent, windowEnv, g.FixedTimeMs)
		gotBytes := make([]byte, len(got))
		for j, r := range got {
			gotBytes[j] = byte(r)
		}
		want, _ := hex.DecodeString(c.Out)
		if !bytes.Equal(gotBytes, want) {
			t.Errorf("generate_rc4_bb_str[%d]: got %x want %x", i, gotBytes, want)
		}
	}
}

func TestAbSign(t *testing.T) {
	g := loadGolden(t)
	windowEnv := "1920|1080|1920|1040|0|30|0|0|1872|92|1920|1040|1857|92|1|24|Win32"
	for i, c := range g.AbSign {
		got := abSignAt(c.Query, c.UserAgent, windowEnv, g.FixedTimeMs)
		if got != c.Out {
			t.Errorf("ab_sign[%d]:\n got %q\nwant %q", i, got, c.Out)
		}
	}
}
