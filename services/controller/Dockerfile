FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod main.go tenant.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /controller .
FROM alpine:3.21
RUN adduser -D -u 10001 app
COPY --from=build /controller /usr/local/bin/controller
USER app
ENV LISTEN_ADDR=0.0.0.0:8080
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/controller"]
