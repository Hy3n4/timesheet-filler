# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder

ARG TARGETPLATFORM
ARG BUILDPLATFORM
ARG TARGETOS
ARG TARGETARCH
RUN apk update && apk add --no-cache git ca-certificates tzdata
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY templates/ ./templates/
COPY translations/ ./translations/
COPY types/ ./types/

RUN if [ -z "$TARGETOS" ]; then TARGETOS=$(go env GOOS); fi && \
    if [ -z "$TARGETARCH" ]; then TARGETARCH=$(go env GOARCH); fi

RUN echo "Building for $TARGETOS/$TARGETARCH"
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags="-w -s" -o /app/timesheet-filler ./cmd/server

FROM gcr.io/distroless/static:nonroot

COPY --from=builder /app/timesheet-filler /app/timesheet-filler

COPY templates/ /app/templates/
COPY translations/ /app/translations/
COPY gorily_timesheet_template_2024.xlsx /app/

WORKDIR /app

EXPOSE 8080 9180

ENV PORT=8080 \
    METRICS_PORT=9180 \
    TEMPLATE_DIR="templates" \
    TEMPLATE_PATH="gorily_timesheet_template_2024.xlsx" \
    EMAIL_ENABLED=false \
    EMAIL_PROVIDER="sendgrid" \
    SENDGRID_API_KEY="" \
    AWS_REGION="us-central-1" \
    AWS_ACCESS_KEY_ID="" \
    AWS_SECRET_ACCESS_KEY="" \
    EMAIL_FROM_NAME="Timesheet Filler" \
    EMAIL_FROM_EMAIL="timesheet@example.com" \
    EMAIL_DEFAULT_RECIPIENTS=""

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/app/timesheet-filler", "-health-check"]

ENTRYPOINT ["/app/timesheet-filler"]
