FROM fedora

RUN dnf install -y poetry

ADD . /app
WORKDIR /app

CMD ["bash", "-c", "sed -i \"s/PORT/${PORT}/g\" pyproject.toml && poetry lock && poetry install --no-root"]
