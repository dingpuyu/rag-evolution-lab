package textutil

import "testing"

func TestTokensPreserveExactIdentifiers(t *testing.T) {
	tokens := Tokens("E1027 与 X-RateLimit-Reset")
	assertContains(t, tokens, "e1027")
	assertContains(t, tokens, "x-ratelimit-reset")
}

func TestHashVectorSemanticAlias(t *testing.T) {
	query := HashVector("员工只登录一次", 256)
	document := HashVector("配置 SSO 单点登录", 256)
	unrelated := HashVector("文件上传大小限制", 256)
	if Cosine(query, document) <= Cosine(query, unrelated) {
		t.Fatalf("semantic alias should rank SSO above unrelated content")
	}
}

func TestCosineRejectsDimensionMismatch(t *testing.T) {
	if got := Cosine([]float64{1}, []float64{1, 2}); got != 0 {
		t.Fatalf("expected zero for mismatched dimensions, got %f", got)
	}
}

func assertContains(t *testing.T, values []string, target string) {
	t.Helper()
	for _, value := range values {
		if value == target {
			return
		}
	}
	t.Fatalf("%q not found in %#v", target, values)
}
