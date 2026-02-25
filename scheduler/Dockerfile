# Build stage
FROM golang:1.21-alpine AS builder

# Install git (required by go mod tidy)
RUN apk add --no-cache git

WORKDIR /app

# Copy go.mod and source code first
COPY go.mod ./
COPY *.go ./

# Generate go.sum with all dependencies (including transitive ones)
RUN go mod tidy

# Build the scheduler binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o nexus-scheduler .

# Runtime stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binary from builder
COPY --from=builder /app/nexus-scheduler .

# Expose metrics port
EXPOSE 9099

# Run the scheduler
CMD ["./nexus-scheduler", "-v=2"]
