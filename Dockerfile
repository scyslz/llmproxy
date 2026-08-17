FROM node:22-alpine AS frontend
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /app/llmproxy ./cmd/llmproxy/...
COPY --from=frontend /app/internal/server/webui/dist /app/internal/server/webui/dist

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/llmproxy .
RUN mkdir -p /app/config /app/logs
EXPOSE 4000
CMD ["./llmproxy"]
