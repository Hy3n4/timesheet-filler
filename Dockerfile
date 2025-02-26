# syntax=docker/dockerfile:1

# ---- Build Stage ----
FROM golang:1.23-alpine AS builder

# Install necessary packages
RUN apk update && apk add --no-cache git

# Set working directory
WORKDIR /app

# Copy go.mod and go.sum files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy the source code
COPY . .

# Build the application
RUN go build -o timesheet-filler

# ---- Run Stage ----
FROM alpine:latest

# Install certificates for HTTPS
RUN apk --no-cache add ca-certificates

# Set working directory
WORKDIR /app

# Copy the compiled binary from the builder stage
COPY --from=builder /app/timesheet-filler .

# Copy templates and necessary files
COPY templates ./templates
COPY gorily_timesheet_template_2024.xlsx .

# Expose port 8080
EXPOSE 8080

# Run the application
CMD ["./timesheet-filler"]
