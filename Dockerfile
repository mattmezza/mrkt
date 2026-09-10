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
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/mrkt ./cmd/mrkt

FROM alpine:3.23.3
RUN apk add --no-cache ca-certificates tzdata && addgroup -S mrkt && adduser -S -G mrkt mrkt
COPY --from=build /out/mrkt /usr/local/bin/mrkt
USER mrkt
EXPOSE 8080
ENTRYPOINT ["mrkt"]
CMD ["server"]
