# syntax=docker/dockerfile:1

# ---- Build Stage ----
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder

# Import build arguments
ARG TARGETPLATFORM
ARG BUILDPLATFORM
ARG TARGETOS
ARG TARGETARCH

# Install necessary packages
RUN apk update && apk add --no-cache git ca-certificates tzdata

# Set working directory
WORKDIR /src

# Copy go.mod and go.sum files first for better caching
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# Copy only necessary source code directories
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY templates/ ./templates/

# Set default TARGETOS and TARGETARCH if not provided
RUN if [ -z "$TARGETOS" ]; then TARGETOS=$(go env GOOS); fi && \
    if [ -z "$TARGETARCH" ]; then TARGETARCH=$(go env GOARCH); fi

# Build the application with proper cross-compilation
RUN echo "Building for $TARGETOS/$TARGETARCH"
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags="-w -s" -o /app/timesheet-filler ./cmd/server

# ---- Final Image Stage ----
# Use distroless instead of scratch for better compatibility across platforms
FROM gcr.io/distroless/static:nonroot

# Copy binary
COPY --from=builder /app/timesheet-filler /app/timesheet-filler

# Copy templates and assets
COPY templates/ /app/templates/
COPY gorily_timesheet_template_2024.xlsx /app/

# Set working directory
WORKDIR /app

# Expose application and metrics ports
EXPOSE 8080 9180

# Setting environment variables
ENV PORT=8080 \
    METRICS_PORT=9180 \
    TEMPLATE_DIR="templates" \
    TEMPLATE_PATH="gorily_timesheet_template_2024.xlsx"

# Define health check (using http-get since distroless doesn't have wget/curl)
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/app/timesheet-filler", "-health-check"]

# Run the application
ENTRYPOINT ["/app/timesheet-filler"]
