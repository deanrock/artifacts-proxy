lazy val root = (project in file("."))
  .settings(
    organization := "com.example",
    name := "quickstart",
    version := "0.0.1-SNAPSHOT",
    scalaVersion := "3.3.3",
    credentials += Credentials("artifacts-proxy", "host.docker.internal", "user", "password"),
    fullResolvers := Seq(
        MavenRepository("maven", "http://host.docker.internal:PORT/maven/").withAllowInsecureProtocol(true),
    )
  )
