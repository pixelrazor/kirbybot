FROM golang:alpine

EXPOSE 8086

WORKDIR /app

COPY . .

RUN go install

ENTRYPOINT [ "kirbybot" ]