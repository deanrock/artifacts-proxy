FROM fedora

RUN dnf install -y nodejs

ADD . /app
WORKDIR /app

CMD ["bash", "-c", "sed -i \"s/PORT/${PORT}/g\" .npmrc && npm ci"]
