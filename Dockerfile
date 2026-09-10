FROM node:24.21.0-alpine3.23 AS assets
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM golang:1.27.1-alpine3.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY --from=assets /src .
ARG MRKT_VERSION=0.1.0-dev
ARG MRKT_COMMIT=unknown
ARG MRKT_BUILD_TIME=unknown
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${MRKT_VERSION} -X main.commit=${MRKT_COMMIT} -X main.built=${MRKT_BUILD_TIME}" -o /out/mrkt ./cmd/mrkt

FROM alpine:3.23.3
ARG MRKT_VERSION=0.1.0-dev
ARG MRKT_COMMIT=unknown
ARG MRKT_BUILD_TIME=unknown
LABEL org.opencontainers.image.title="mrkt" \
      org.opencontainers.image.version="${MRKT_VERSION}" \
      org.opencontainers.image.revision="${MRKT_COMMIT}" \
      org.opencontainers.image.licenses="MIT"
RUN apk add --no-cache ca-certificates tzdata && addgroup -S mrkt && adduser -S -G mrkt mrkt
COPY --from=build /out/mrkt /usr/local/bin/mrkt
USER mrkt
EXPOSE 8080
ENTRYPOINT ["mrkt"]
CMD ["server"]
