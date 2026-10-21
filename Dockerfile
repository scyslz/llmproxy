FROM node:22-alpine AS frontend
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder
ARG TARGETOS TARGETARCH
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY --from=frontend /app/internal/server/webui/dist /app/internal/server/webui/dist
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /app/llmproxy ./cmd/llmproxy/...

FROM alpine:3.21 AS runtime
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY config/ ./config/
RUN mkdir -p /app/logs
EXPOSE 3000

FROM runtime AS from-prebuilt
ARG TARGETARCH
COPY .ci/linux/${TARGETARCH}/llmproxy /app/llmproxy
RUN chmod +x /app/llmproxy
CMD ["./llmproxy"]

FROM runtime AS from-source
COPY --from=builder /app/llmproxy /app/llmproxy
CMD ["./llmproxy"]
