// Package absign 实现抖音 web API 的 a_bogus 签名。
// 移植自 yt-dlp fork (douyin-live-support 分支) yt_dlp/extractor/tiktok.py 的
// _douyin_ab_sign 及其依赖函数，逐字节对照验证。
//
// 关键移植点：Python 把 RC4 输出当 "码点序列 str"（每个 char 一个 0-255 码点，
// 非 UTF-8 字节序列）。Go 用 []rune 承载码点序列以保持一致。
package absign

import (
	"math"
	"strings"
	"time"
)

// rc4Encrypt 对应 Python _douyin_rc4_encrypt。
// plaintext / key 均为码点序列（[]rune），返回码点序列。
func rc4Encrypt(plaintext, key []rune) []rune {
	var s [256]int
	for i := 0; i < 256; i++ {
		s[i] = i
	}
	j := 0
	for i := 0; i < 256; i++ {
		j = (j + s[i] + int(key[i%len(key)])) % 256
		s[i], s[j] = s[j], s[i]
	}
	i := 0
	j = 0
	result := make([]rune, 0, len(plaintext))
	for _, ch := range plaintext {
		i = (i + 1) % 256
		j = (j + s[i]) % 256
		s[i], s[j] = s[j], s[i]
		t := (s[i] + s[j]) % 256
		result = append(result, rune(s[t]^int(ch)))
	}
	return result
}

// leftRotate 对应 Python _douyin_left_rotate（32 位循环左移）。
func leftRotate(x, n uint32) uint32 {
	n %= 32
	if n == 0 {
		return x
	}
	return (x << n) | (x >> (32 - n))
}

func getTj(j int) uint32 {
	if j < 16 {
		return 2043430169
	}
	return 2055708042
}

func ffJ(j int, x, y, z uint32) uint32 {
	if j < 16 {
		return x ^ y ^ z
	}
	return (x & y) | (x & z) | (y & z)
}

func ggJ(j int, x, y, z uint32) uint32 {
	if j < 16 {
		return x ^ y ^ z
	}
	return (x & y) | (^x & z)
}

// SM3 对应 Python _DouyinSM3。
type SM3 struct {
	reg   [8]uint32
	chunk []byte
	size  int
}

func NewSM3() *SM3 {
	s := &SM3{}
	s.reset()
	return s
}

func (s *SM3) reset() {
	s.reg = [8]uint32{
		1937774191, 1226093241, 388252375, 3666478592,
		2842636476, 372324522, 3817729613, 2969243214,
	}
	s.chunk = nil
	s.size = 0
}

// write 对应 Python _DouyinSM3.write。data 为字节序列。
func (s *SM3) write(data []byte) {
	s.size += len(data)
	f := 64 - len(s.chunk)
	if len(data) < f {
		s.chunk = append(s.chunk, data...)
		return
	}
	s.chunk = append(s.chunk, data[:f]...)
	for len(s.chunk) >= 64 {
		s.compress(s.chunk)
		if f < len(data) {
			end := f + 64
			if end > len(data) {
				end = len(data)
			}
			s.chunk = append([]byte(nil), data[f:end]...)
		} else {
			s.chunk = nil
		}
		f += 64
	}
}

// fill 对应 Python _DouyinSM3._fill。
func (s *SM3) fill() {
	bitLength := uint64(s.size) * 8
	paddingPos := len(s.chunk)
	s.chunk = append(s.chunk, 0x80)
	paddingPos = (paddingPos + 1) % 64
	if 64-paddingPos < 8 {
		paddingPos -= 64
	}
	for paddingPos < 56 {
		s.chunk = append(s.chunk, 0)
		paddingPos++
	}
	// 64-bit big-endian 长度（Python: high_bits 4 字节 + low 4 字节）
	for i := 0; i < 8; i++ {
		s.chunk = append(s.chunk, byte(bitLength>>(56-8*i)))
	}
}

// compress 对应 Python _DouyinSM3._compress，要求 len(data)>=64。
func (s *SM3) compress(data []byte) {
	var w [132]uint32
	for t := 0; t < 16; t++ {
		w[t] = uint32(data[4*t])<<24 | uint32(data[4*t+1])<<16 | uint32(data[4*t+2])<<8 | uint32(data[4*t+3])
	}
	for j := 16; j < 68; j++ {
		a := w[j-16] ^ w[j-9] ^ leftRotate(w[j-3], 15)
		a = a ^ leftRotate(a, 15) ^ leftRotate(a, 23)
		w[j] = a ^ leftRotate(w[j-13], 7) ^ w[j-6]
	}
	for j := 0; j < 64; j++ {
		w[j+68] = w[j] ^ w[j+4]
	}
	a, b, c, d, e, f, g, h := s.reg[0], s.reg[1], s.reg[2], s.reg[3], s.reg[4], s.reg[5], s.reg[6], s.reg[7]
	for j := 0; j < 64; j++ {
		ss1 := leftRotate(leftRotate(a, 12)+e+leftRotate(getTj(j), uint32(j)), 7)
		ss2 := ss1 ^ leftRotate(a, 12)
		tt1 := ffJ(j, a, b, c) + d + ss2 + w[j+68]
		tt2 := ggJ(j, e, f, g) + h + ss1 + w[j]
		d = c
		c = leftRotate(b, 9)
		b = a
		a = tt1
		h = g
		g = leftRotate(f, 19)
		f = e
		e = tt2 ^ leftRotate(tt2, 9) ^ leftRotate(tt2, 17)
	}
	s.reg[0] ^= a
	s.reg[1] ^= b
	s.reg[2] ^= c
	s.reg[3] ^= d
	s.reg[4] ^= e
	s.reg[5] ^= f
	s.reg[6] ^= g
	s.reg[7] ^= h
}

// Sum 对应 Python _DouyinSM3.sum。data 非 nil 时先 reset+write。返回 32 字节摘要。
func (s *SM3) Sum(data []byte) []byte {
	if data != nil {
		s.reset()
		s.write(data)
	}
	s.fill()
	for i := 0; i < len(s.chunk); i += 64 {
		end := i + 64
		if end > len(s.chunk) {
			end = len(s.chunk)
		}
		s.compress(s.chunk[i:end])
	}
	out := make([]byte, 32)
	for f := 0; f < 8; f++ {
		c := s.reg[f]
		out[f*4] = byte(c >> 24)
		out[f*4+1] = byte(c >> 16)
		out[f*4+2] = byte(c >> 8)
		out[f*4+3] = byte(c)
	}
	s.reset()
	return out
}

// SumHex 返回十六进制摘要。
func (s *SM3) SumHex(data []byte) string {
	out := s.Sum(data)
	var sb strings.Builder
	for _, b := range out {
		sb.WriteString(padHex(b))
	}
	return sb.String()
}

func padHex(b byte) string {
	const hexd = "0123456789abcdef"
	return string([]byte{hexd[b>>4], hexd[b&0xf]})
}

var encodingTables = map[string]string{
	"s0": "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=",
	"s1": "Dkdpgh4ZKsQB80/Mfvw36XI1R25+WUAlEi7NLboqYTOPuzmFjJnryx9HVGcaStCe=",
	"s2": "Dkdpgh4ZKsQB80/Mfvw36XI1R25-WUAlEi7NLboqYTOPuzmFjJnryx9HVGcaStCe=",
	"s3": "ckdp1h4ZKsUB80/Mfvw36XIgR25+WQAlEi7NLboqYTOPuzmFjJnryx9HVGDaStCe",
	"s4": "Dkdpgh2ZmsQB80/MfvV36XI1R45-WUAlEixNLwoqYTOPuzKFjJnry79HbGcaStCe",
}

// resultEncrypt 对应 Python _douyin_result_encrypt。longStr 为码点序列。
func resultEncrypt(longStr []rune, num string) string {
	table := encodingTables[num]
	masks := []int{16515072, 258048, 4032, 63}
	shifts := []uint{18, 12, 6, 0}
	var sb strings.Builder
	roundNum := 0
	longInt := getLongInt(0, longStr)
	totalChars := int(math.Ceil(float64(len(longStr)) / 3.0 * 4.0))
	for i := 0; i < totalChars; i++ {
		if i/4 != roundNum {
			roundNum++
			longInt = getLongInt(roundNum, longStr)
		}
		index := i % 4
		charIndex := (longInt & masks[index]) >> shifts[index]
		sb.WriteByte(table[charIndex])
	}
	return sb.String()
}

func getLongInt(roundNum int, longStr []rune) int {
	roundNum *= 3
	var c1, c2, c3 int
	if roundNum < len(longStr) {
		c1 = int(longStr[roundNum])
	}
	if roundNum+1 < len(longStr) {
		c2 = int(longStr[roundNum+1])
	}
	if roundNum+2 < len(longStr) {
		c3 = int(longStr[roundNum+2])
	}
	return (c1 << 16) | (c2 << 8) | c3
}

func generRandom(randomNum int, option [2]int) [4]int {
	byte1 := randomNum & 255
	byte2 := (randomNum >> 8) & 255
	return [4]int{
		(byte1 & 170) | (option[0] & 85),
		(byte1 & 85) | (option[0] & 170),
		(byte2 & 170) | (option[1] & 85),
		(byte2 & 85) | (option[1] & 170),
	}
}

// generateRandomStr 对应 Python _douyin_generate_random_str（固定输出）。
func generateRandomStr() []rune {
	randomValues := []float64{0.123456789, 0.987654321, 0.555555555}
	r0 := generRandom(int(randomValues[0]*10000), [2]int{3, 45})
	r1 := generRandom(int(randomValues[1]*10000), [2]int{1, 0})
	r2 := generRandom(int(randomValues[2]*10000), [2]int{1, 5})
	randomBytes := make([]int, 0, 12)
	randomBytes = append(randomBytes, r0[:]...)
	randomBytes = append(randomBytes, r1[:]...)
	randomBytes = append(randomBytes, r2[:]...)
	out := make([]rune, len(randomBytes))
	for i, b := range randomBytes {
		out[i] = rune(b)
	}
	return out
}

// generateRc4BbStr 对应 Python _douyin_generate_rc4_bb_str。
// startTimeMs 注入以便测试（Python 内部用 int(time.time()*1000)）。
func generateRc4BbStr(urlSearchParams, userAgent, windowEnvStr string, startTimeMs int64) []rune {
	arguments := [3]int{0, 1, 14}
	suffix := "cus"
	sm3 := NewSM3()

	urlSearchParamsList := sm3.Sum(sm3.Sum([]byte(urlSearchParams + suffix)))
	cus := sm3.Sum(sm3.Sum([]byte(suffix)))
	uaKey := []rune{0, 1, 14}
	uaEnc := rc4Encrypt([]rune(userAgent), uaKey)
	ua := sm3.Sum([]byte(resultEncrypt(uaEnc, "s3")))

	endTime := startTimeMs + 100
	splitToBytes := func(num int64) [4]byte {
		return [4]byte{
			byte(num >> 24),
			byte(num >> 16),
			byte(num >> 8),
			byte(num),
		}
	}

	// b[15] 嵌套结构
	const aid = 6383
	const pageID = 110624

	startTimeBytes := splitToBytes(startTimeMs)
	endTimeBytes := splitToBytes(endTime)

	// 按 Python b[k] 赋值（k 离散）
	b8 := byte(3)
	b10 := endTime
	b16 := startTimeMs
	b18 := byte(44)
	b20 := startTimeBytes[0]
	b21 := startTimeBytes[1]
	b22 := startTimeBytes[2]
	b23 := startTimeBytes[3]
	b24 := byte(uint64(b16) >> 32)
	b25 := byte(uint64(b16) >> 40)

	arg0Bytes := splitToBytes(int64(arguments[0]))
	b26 := arg0Bytes[0]
	b27 := arg0Bytes[1]
	b28 := arg0Bytes[2]
	b29 := arg0Bytes[3]
	b30 := byte(arguments[1] / 256)
	b31 := byte(arguments[1] % 256)
	arg1Bytes := splitToBytes(int64(arguments[1]))
	b32 := arg1Bytes[0]
	b33 := arg1Bytes[1]
	arg2Bytes := splitToBytes(int64(arguments[2]))
	b34 := arg2Bytes[0]
	b35 := arg2Bytes[1]
	b36 := arg2Bytes[2]
	b37 := arg2Bytes[3]

	b38 := urlSearchParamsList[21]
	b39 := urlSearchParamsList[22]
	b40 := cus[21]
	b41 := cus[22]
	b42 := ua[23]
	b43 := ua[24]

	b44 := endTimeBytes[0]
	b45 := endTimeBytes[1]
	b46 := endTimeBytes[2]
	b47 := endTimeBytes[3]
	b48 := b8
	b49 := byte(uint64(b10) >> 32)
	b50 := byte(uint64(b10) >> 40)
	b51 := int64(pageID)
	pageIDBytes := splitToBytes(b51)
	b52 := pageIDBytes[0]
	b53 := pageIDBytes[1]
	b54 := pageIDBytes[2]
	b55 := pageIDBytes[3]
	// b[56] = aid 在 Python 中赋值后从未读取，省略
	b57 := byte(aid & 255)
	b58 := byte((aid >> 8) & 255)
	b59 := byte((aid >> 16) & 255)
	b60 := byte((aid >> 24) & 255)

	windowEnvList := []byte(windowEnvStr)
	b64 := len(windowEnvList)
	b65 := b64 & 255
	b66 := (b64 >> 8) & 255
	b70 := byte(0)
	b71 := byte(0)

	b72 := b18 ^ b20 ^ b26 ^ b30 ^ b38 ^ b40 ^ b42 ^ b21 ^ b27 ^ b31 ^
		b35 ^ b39 ^ b41 ^ b43 ^ b22 ^ b28 ^ b32 ^ b36 ^ b23 ^ b29 ^
		b33 ^ b37 ^ b44 ^ b45 ^ b46 ^ b47 ^ b48 ^ b49 ^ b50 ^ b24 ^
		b25 ^ b52 ^ b53 ^ b54 ^ b55 ^ b57 ^ b58 ^ b59 ^ b60 ^ byte(b65) ^ byte(b66) ^ b70 ^ b71

	bb := []byte{
		b18, b20, b52, b26, b30, b34, b58, b38, b40, b53, b42, b21,
		b27, b54, b55, b31, b35, b57, b39, b41, b43, b22, b28, b32,
		b60, b36, b23, b29, b33, b37, b44, b45, b59, b46, b47, b48,
		b49, b50, b24, b25, byte(b65), byte(b66), b70, b71,
	}
	bb = append(bb, windowEnvList...)
	bb = append(bb, b72)

	bbRunes := make([]rune, len(bb))
	for i, by := range bb {
		bbRunes[i] = rune(by)
	}
	return rc4Encrypt(bbRunes, []rune{121})
}

// AbSign 对应 Python _douyin_ab_sign，使用当前时间戳。
func AbSign(urlSearchParams, userAgent string) string {
	windowEnvStr := "1920|1080|1920|1040|0|30|0|0|1872|92|1920|1040|1857|92|1|24|Win32"
	return abSignAt(urlSearchParams, userAgent, windowEnvStr, time.Now().UnixMilli())
}

// abSignAt 使用注入的时间戳，便于测试对照。
func abSignAt(urlSearchParams, userAgent, windowEnvStr string, startTimeMs int64) string {
	concat := append(generateRandomStr(), generateRc4BbStr(urlSearchParams, userAgent, windowEnvStr, startTimeMs)...)
	return resultEncrypt(concat, "s4") + "="
}
