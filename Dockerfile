FROM golang:1.12 as builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 go build -a -installsuffix cgo -o recursed .

FROM alpine:3.9
WORKDIR /app
EXPOSE 8080
ENV JAEGER_SERVICE_NAME=recursed
COPY --from=builder /app/recursed .
CMD ["./recursed"]

