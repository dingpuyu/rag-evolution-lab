# syntax=docker/dockerfile:1

FROM golang:1.23-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/raglab ./cmd/raglab

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /out/raglab /usr/local/bin/raglab
COPY go.mod go.sum ./
COPY datasets ./datasets
COPY migrations ./migrations

ENV RAGLAB_MILVUS_URL=http://milvus:19530 \
    RAGLAB_POSTGRES_URL=postgres://raglab:raglab-local@postgres:5432/raglab?sslmode=disable \
    RAGLAB_EMBEDDING_BACKEND=hash \
    RAGLAB_HASH_EMBEDDING_DIMENSIONS=512 \
    RAGLAB_GENERATION_PROVIDER=extractive \
    RAGLAB_LIFECYCLE_COLLECTION=raglab_lifecycle_v1 \
    RAGLAB_LIFECYCLE_ALIAS=raglab_knowledge_active \
    RAGLAB_LIFECYCLE_STATE=/var/lib/raglab/lifecycle/state.json \
    RAGLAB_INGESTION_JOB_STATE=/var/lib/raglab/ingestion/jobs.json \
    RAGLAB_AUTH_ACCOUNTS=/var/lib/raglab/auth/accounts.json

RUN mkdir -p /var/lib/raglab/lifecycle /var/lib/raglab/ingestion /var/lib/raglab/auth

EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=10 \
    CMD curl --fail --silent http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/raglab"]
CMD ["serve-lab", "--addr", "0.0.0.0:8080"]
