FROM ubuntu:jammy

COPY sources.list /etc/apt/sources.list

CMD ["bash", "-c", "sed -i \"s/PORT/${PORT}/g\" /etc/apt/sources.list && apt-get update && apt-get install -y wget curl"]
