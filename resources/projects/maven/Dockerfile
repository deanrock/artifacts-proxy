FROM sbtscala/scala-sbt:eclipse-temurin-alpine-24.0.1_9_1.12.11_2.12.21

ADD . /app
WORKDIR /app

# init sbt
RUN sbt help

CMD ["bash", "-c", "sed -i \"s/PORT/${PORT}/g\" build.sbt && sbt run"]
