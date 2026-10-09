# syntax=docker/dockerfile:1

FROM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine AS dev
# qpdf validates generated files in the tests.
RUN apk add --no-cache qpdf
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .

FROM dev AS build
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM dev AS test
RUN --mount=type=cache,target=/root/.cache/go-build \
    go vet ./... && go test ./...

FROM gcr.io/distroless/static-debian12:nonroot AS prod
COPY --from=build /out/server /app/server
COPY --from=web /web/dist /app/web
COPY fonts /app/fonts
ENV ADDR=0.0.0.0:8080 DATA_DIR=/data WEB_DIR=/app/web BUILTIN_FONTS=/app/fonts SYSTEM_FONTS=/fonts
EXPOSE 8080
ENTRYPOINT ["/app/server"]
