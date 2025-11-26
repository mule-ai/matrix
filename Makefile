.PHONY: build run test clean

# Binary name
BINARY_NAME=matrix-microservice

# Build the binary
build:
	go build -o ${BINARY_NAME} ./cmd/matrix

# Run the service
run: build
	./${BINARY_NAME}

# Run tests
test:
	go test -v ./...

# Clean build artifacts
clean:
	rm -f ${BINARY_NAME}

# Install dependencies
deps:
	go mod tidy

# Build Docker image
docker-build:
	docker build -t matrix-microservice .

# Run Docker container
docker-run:
	docker run -p 8080:8080 matrix-microservice