# Start from a Golang base image
FROM golang:1.22

# Set the Current Working Directory inside the container
WORKDIR /app

# Copy go mod and sum files
COPY . .

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod tidy
RUN go mod download

# Build the Go app
RUN go build -o lnd-dbreader .

# Command to keep the container running
# CMD ["sleep", "infinity"]
CMD ["./lnd-dbreader"]
