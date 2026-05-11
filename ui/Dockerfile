FROM docker.io/library/node:22-alpine AS builder
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY ui/ ./ui/
ARG VITE_MANAGER_GRAPHQL_URL=http://localhost:8080/graphql
ARG VITE_MANAGER_GRAPHQL_WS_URL=ws://localhost:8080/subscriptions
ENV VITE_MANAGER_GRAPHQL_URL=${VITE_MANAGER_GRAPHQL_URL}
ENV VITE_MANAGER_GRAPHQL_WS_URL=${VITE_MANAGER_GRAPHQL_WS_URL}
RUN npm run typecheck
RUN npm run build

FROM docker.io/library/nginx:1.27-alpine AS runtime
COPY --from=builder /src/ui/dist /usr/share/nginx/html
EXPOSE 80
