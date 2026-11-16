FROM --platform=$BUILDPLATFORM golang:1.27.4-bookworm AS build
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd cmd
COPY internal internal
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /ics ./cmd/ics

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /ics /app/ics
# eval reads the paraphrases from the working directory.
COPY eval eval
USER nonroot
EXPOSE 8080
ENTRYPOINT ["/app/ics"]
CMD ["serve"]
