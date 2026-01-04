# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go.mod first for caching
COPY go.mod ./
RUN go mod download

# Copy source code
COPY *.go ./

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
