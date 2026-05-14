FROM docker.io/library/node:22-alpine AS builder
WORKDIR /src
COPY package.json package-lock.json ./
COPY ui/package.json ./ui/package.json
RUN npm ci
COPY ui/ ./ui/
RUN npm run typecheck
RUN npm run build

FROM docker.io/library/nginx:1.27-alpine AS runtime
COPY --from=builder /src/ui/dist /usr/share/nginx/html
EXPOSE 80
