FROM python:alpine

RUN pip install poetry

ADD . /app
WORKDIR /app

CMD ["sh", "-c", "sed -i \"s/PORT/${PORT}/g\" pyproject.toml && poetry lock && poetry install --no-root"]
