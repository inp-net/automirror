# Use the official Go image for building the application
FROM golang:1.23-alpine AS build

LABEL org.opencontainers.image.source=https://github.com/inp-net/automirror
LABEL org.opencontainers.image.description="Automatically create push-mirrors from a gitlab instance to a github organization for public repositories having a certain topic"
LABEL org.opencontainers.image.licenses="MIT"

# Set the working directory inside the container
WORKDIR /app

# Copy the Go modules files
COPY go.mod go.sum ./

# Download Go module dependencies
RUN go mod download

# Copy the source code into the container
COPY . .

# Build the Go application
RUN go build -o automirror .

# Use a minimal base image to run the app
FROM alpine:3.20

# Set the working directory inside the container
WORKDIR /app

# Install git
RUN apk add --no-cache git

# Copy the compiled binary from the build stage
COPY --from=build /app/automirror /app/automirror

# Run the compiled Go application
CMD ["/app/automirror"]
