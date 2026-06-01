FROM node:alpine

ADD . /app
WORKDIR /app

ENTRYPOINT []
CMD ["sh", "-c", "sed -i \"s/PORT/${PORT}/g\" .npmrc && npm ci"]
