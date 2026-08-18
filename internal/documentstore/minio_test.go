package documentstore

import "testing"

func TestParseObjectURI(t *testing.T) {
	bucket, key, err := parseObjectURI("s3://raglab-documents/tenant_a/data/doc/r1/manual.document-ir.json")
	if err != nil {
		t.Fatalf("parse object URI: %v", err)
	}
	if bucket != "raglab-documents" || key != "tenant_a/data/doc/r1/manual.document-ir.json" {
		t.Fatalf("unexpected object location: %q %q", bucket, key)
	}
}

func TestParseObjectURIRejectsUntrustedLocations(t *testing.T) {
	for _, raw := range []string{"https://example.com/file", "s3:///missing-bucket", "s3://user:pass@bucket/file", "s3://bucket/", "s3://bucket/a/../secret", "s3://bucket/file?version=old", "s3://bucket/file#fragment"} {
		if _, _, err := parseObjectURI(raw); err == nil {
			t.Fatalf("URI should be rejected: %s", raw)
		}
	}
}
