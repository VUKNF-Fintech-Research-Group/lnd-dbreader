# Start from a Golang base image
FROM golang:1.23

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy go mod and sum files
COPY . .

# Download all dependencies
RUN go mod tidy
RUN go mod download

# Build the Go app
RUN go build -o lnd-dbreader .

CMD ["./lnd-dbreader"]
