package textutil

import (
	"hash/fnv"
	"math"
	"strings"
	"unicode"
)

var semanticAliases = map[string]string{
	"单点登录":   "sso",
	"企业登录":   "sso",
	"登录集成":   "sso",
	"只登录一次":  "sso",
	"调用频率":   "限流",
	"频率限制":   "限流",
	"请求过多":   "限流",
	"太频繁":    "限流",
	"收费":     "计费",
	"价格":     "计费",
	"异地备份":   "跨区域备份",
	"跨区备份":   "跨区域备份",
	"另一个地区":  "跨区域",
	"导出所有租户": "跨租户导出",
}

func NormalizeSemantic(text string) string {
	text = strings.ToLower(text)
	for from, to := range semanticAliases {
		text = strings.ReplaceAll(text, from, to)
	}
	return text
}

func Tokens(text string) []string {
	text = strings.ToLower(text)
	var tokens []string
	var ascii []rune
	var cjk []rune

	flushASCII := func() {
		if len(ascii) > 0 {
			tokens = append(tokens, string(ascii))
			ascii = ascii[:0]
		}
	}
	flushCJK := func() {
		if len(cjk) == 0 {
			return
		}
		if len(cjk) == 1 {
			tokens = append(tokens, string(cjk))
		}
		for i := 0; i+1 < len(cjk); i++ {
			tokens = append(tokens, string(cjk[i:i+2]))
		}
		cjk = cjk[:0]
	}

	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			flushASCII()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.':
			flushCJK()
			ascii = append(ascii, r)
		default:
			flushASCII()
			flushCJK()
		}
	}
	flushASCII()
	flushCJK()
	return tokens
}

func TermFrequency(tokens []string) map[string]float64 {
	result := make(map[string]float64, len(tokens))
	for _, token := range tokens {
		result[token]++
	}
	return result
}

func HashVector(text string, dimensions int) []float64 {
	if dimensions <= 0 {
		dimensions = 256
	}
	vector := make([]float64, dimensions)
	for token, count := range TermFrequency(Tokens(NormalizeSemantic(text))) {
		h := fnv.New64a()
		_, _ = h.Write([]byte(token))
		value := h.Sum64()
		idx := int(value % uint64(dimensions))
		sign := 1.0
		if value&(1<<63) != 0 {
			sign = -1
		}
		vector[idx] += sign * (1 + math.Log(count))
	}
	normalize(vector)
	return vector
}

func Cosine(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

func normalize(vector []float64) {
	var norm float64
	for _, value := range vector {
		norm += value * value
	}
	if norm == 0 {
		return
	}
	norm = math.Sqrt(norm)
	for i := range vector {
		vector[i] /= norm
	}
}
