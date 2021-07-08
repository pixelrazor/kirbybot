FROM golang:alpine

VOLUME [ "/data" ]

WORKDIR /app

COPY . .

RUN go install

ENTRYPOINT [ "kirbybot" ]