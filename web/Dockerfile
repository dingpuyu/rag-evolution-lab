# syntax=docker/dockerfile:1

FROM node:22-bookworm-slim AS builder

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --include=optional \
    && WORKERD_VERSION="$(node -p "require('./node_modules/workerd/package.json').version")" \
    && case "$(dpkg --print-architecture)" in \
         arm64) WORKERD_PACKAGE='@cloudflare/workerd-linux-arm64' ;; \
         amd64) WORKERD_PACKAGE='@cloudflare/workerd-linux-64' ;; \
         *) echo 'unsupported container architecture for workerd' >&2; exit 1 ;; \
       esac \
    && npm install --no-save "${WORKERD_PACKAGE}@${WORKERD_VERSION}"
COPY . .

ARG NEXT_PUBLIC_API_BASE=http://localhost:8080
ENV NEXT_PUBLIC_API_BASE=${NEXT_PUBLIC_API_BASE}
ARG NEXT_PUBLIC_AGENT_API_BASE=http://localhost:8090
ENV NEXT_PUBLIC_AGENT_API_BASE=${NEXT_PUBLIC_AGENT_API_BASE}
ARG NEXT_PUBLIC_AGENT_EVALUATION_URL=http://localhost:18200
ENV NEXT_PUBLIC_AGENT_EVALUATION_URL=${NEXT_PUBLIC_AGENT_EVALUATION_URL}
RUN npm run build

FROM node:22-bookworm-slim

WORKDIR /app
ENV NODE_ENV=production PORT=3000 HOSTNAME=0.0.0.0
COPY --from=builder /app /app

EXPOSE 3000
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=10 \
    CMD node -e "fetch('http://127.0.0.1:3000/').then(r => process.exit(r.ok ? 0 : 1)).catch(() => process.exit(1))"

CMD ["npm", "run", "start", "--", "--hostname", "0.0.0.0", "--port", "3000"]
