FROM golang:1.24-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o matrix-microservice ./cmd/matrix

# Use distroless as minimal base image
FROM gcr.io/distroless/static:nonroot

WORKDIR /

# Copy the binary
COPY --from=builder /app/matrix-microservice .

# Copy the config file
COPY --from=builder /app/config.yaml ./config.yaml

USER nonroot:nonroot

ENTRYPOINT ["./matrix-microservice"]