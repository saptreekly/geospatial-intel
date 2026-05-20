# Stage 1: Build spatial_engine (Rust)
FROM rust:1.80-alpine AS rust-builder
RUN apk add --no-cache musl-dev
WORKDIR /build
COPY spatial_engine/Cargo.toml spatial_engine/Cargo.lock ./
COPY spatial_engine/src ./src
RUN cargo build --release

# Stage 2: Build Go Server
FROM golang:1.26.2-alpine AS go-builder
RUN apk add --no-cache build-base
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Copy the compiled Rust library
COPY --from=rust-builder /build/target/release/libspatial_engine.so /app/spatial_engine/target/release/libspatial_engine.so

# Build the Go application with CGO
ENV CGO_ENABLED=1
ENV LD_LIBRARY_PATH=/app/spatial_engine/target/release
RUN go build -o geospatial-server .

# Stage 3: Minimal Runtime
FROM alpine:3.19
RUN apk add --no-cache libgcc
WORKDIR /app
COPY --from=go-builder /app/geospatial-server .
COPY --from=go-builder /app/spatial_engine/target/release/libspatial_engine.so /usr/lib/libspatial_engine.so

ENV LD_LIBRARY_PATH=/usr/lib
EXPOSE 8080
ENTRYPOINT ["./geospatial-server"]
