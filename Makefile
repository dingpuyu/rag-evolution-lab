.PHONY: fmt test validate ingest eval compare compare-metadata

fmt:
	gofmt -w cmd internal

test:
	go test ./...

validate:
	go run ./cmd/raglab validate

ingest:
	go run ./cmd/raglab ingest

eval:
	go run ./cmd/raglab eval --pipeline v0-keyword --split development

compare:
	go run ./cmd/raglab compare --baseline v0-keyword --candidate v1-vector --split development

compare-metadata:
	go run ./cmd/raglab compare --baseline v0-keyword --candidate v2-metadata --split development
