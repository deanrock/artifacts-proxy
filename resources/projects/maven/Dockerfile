FROM fedora

RUN curl -L https://www.scala-sbt.org/sbt-rpm.repo > sbt-rpm.repo && \
    mv sbt-rpm.repo /etc/yum.repos.d/ && \
    dnf install -y sbt java-25-openjdk-devel

ADD . /app
WORKDIR /app

# init sbt
RUN sbt help

CMD ["bash", "-c", "sed -i \"s/PORT/${PORT}/g\" build.sbt && sbt run"]
