FROM ghcr.io/cirruslabs/flutter:stable AS builder
WORKDIR /src
COPY ui/pubspec.yaml ui/pubspec.lock* ./
RUN flutter pub get
COPY ui/ .
ARG MANAGER_GRAPHQL_URL=http://localhost:8080/graphql
RUN flutter test --coverage
RUN flutter build web --release --dart-define=MANAGER_GRAPHQL_URL=${MANAGER_GRAPHQL_URL}

FROM docker.io/library/nginx:1.27-alpine AS runtime
COPY --from=builder /src/build/web /usr/share/nginx/html
EXPOSE 80
