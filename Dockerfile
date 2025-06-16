FROM golang:1.21-alpine AS builder

WORKDIR /build

# Install dependencies
RUN apk add --no-cache git make

# Copy all source files
COPY . .

# Download dependencies and create go.sum
RUN go mod download && go mod tidy

# Build binaries
RUN make build-all

FROM scratch
COPY --from=builder /build/dist/* /