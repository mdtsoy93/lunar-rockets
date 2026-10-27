FROM golang:1.26.7-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/rockets ./cmd/rockets

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
USER app
COPY --from=build /out/rockets /usr/local/bin/rockets
EXPOSE 8088
ENTRYPOINT ["/usr/local/bin/rockets"]
