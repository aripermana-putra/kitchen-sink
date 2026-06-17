FROM golang:1.22-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o server .

FROM alpine:3.19
WORKDIR /app
COPY --from=build /app/server .
EXPOSE 8082
# Point to the OTel Collector sidecar / service.
ENV OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317
ENV OTEL_SERVICE_NAME=02-otel-sdk
ENTRYPOINT ["./server"]
