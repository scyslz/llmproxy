FROM node:22-alpine AS builder

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM node:22-alpine

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev
COPY --from=builder /app/dist ./dist

ENV NODE_ENV=production

RUN mkdir -p /app/config /app/logs && chown -R node:node /app

EXPOSE 4000
USER node
VOLUME [ "/app/config" ]
CMD ["node", "dist/server.cjs"]
