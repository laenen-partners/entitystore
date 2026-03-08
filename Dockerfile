# Build stage
FROM golang:1.25 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -o /estore ./cmd/estore

# Run stage
FROM gcr.io/distroless/static-debian12

COPY --from=build /estore /estore

EXPOSE 3002

ENTRYPOINT ["/estore"]
