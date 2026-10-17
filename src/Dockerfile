# Target architecture for the final image. ECS Fargate runs X86_64 unless the
# task definition says otherwise, so default to amd64 on every machine.
# For a native image on Apple Silicon: --build-arg TARGET_ARCH=arm64
ARG TARGET_ARCH=amd64

# ---- Build stage ----
FROM golang:1.27-alpine AS builder
ARG TARGET_ARCH

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY product-service.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGET_ARCH} go build -ldflags="-s -w" -o product-service .

# ---- Run stage ----
FROM --platform=linux/${TARGET_ARCH} alpine:latest

WORKDIR /app

COPY --from=builder /app/product-service .

# Run as the unprivileged "nobody" user that ships with Alpine.
USER 65534:65534

ENV PORT=8080

EXPOSE 8080

CMD ["./product-service"]
