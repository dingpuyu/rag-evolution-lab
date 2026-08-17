package httpapi

import "testing"

func TestNormalizeSourceMetadata(t *testing.T) {
	metadata := documentUploadMetadata{
		SourceType:         "official_manufacturer",
		SourceURLs:         []string{" https://example.com/product#overview ", "https://example.com/product"},
		CollectedAt:        "2026-08-17",
		SourceReviewStatus: "approved",
		SourceReviewedAt:   "2026-08-17T08:00:00Z",
	}
	if err := normalizeSourceMetadata(&metadata); err != nil {
		t.Fatalf("normalize source metadata: %v", err)
	}
	if len(metadata.SourceURLs) != 1 || metadata.SourceURLs[0] != "https://example.com/product" {
		t.Fatalf("unexpected normalized URLs: %#v", metadata.SourceURLs)
	}
}

func TestNormalizeSourceMetadataRejectsUnsafeOrUnreviewedClaims(t *testing.T) {
	tests := []documentUploadMetadata{
		{SourceType: "official", SourceURLs: []string{"http://example.com/manual"}},
		{SourceType: "official", SourceURLs: []string{"https://localhost/manual"}},
		{SourceURLs: []string{"https://example.com/manual"}},
		{SourceReviewStatus: "approved"},
		{CollectedAt: "17-08-2026"},
	}
	for index := range tests {
		if err := normalizeSourceMetadata(&tests[index]); err == nil {
			t.Fatalf("case %d should be rejected: %#v", index, tests[index])
		}
	}
}

func TestIngestionMetadataHashChangesWithRetrievalScope(t *testing.T) {
	base := documentUploadMetadata{
		Title: "兼容矩阵", Version: "2.6", Domain: "medical-device",
		ProductFamily: "pulsecare-vsm100-family", ModelCodes: []string{"VSM-100"},
		Region: "CN", Language: "zh-CN",
	}
	first := ingestionMetadataHash(base)
	if first == "" || first != ingestionMetadataHash(base) {
		t.Fatal("ingestion metadata hash must be stable")
	}
	base.ModelCodes = append(base.ModelCodes, "VSM-100 Pro")
	if first == ingestionMetadataHash(base) {
		t.Fatal("model scope changes must invalidate ingestion metadata hash")
	}
}
