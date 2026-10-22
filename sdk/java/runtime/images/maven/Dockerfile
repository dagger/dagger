FROM maven:3.9.9-eclipse-temurin-21-alpine@sha256:4cbb8bf76c46b97e028998f2486ed014759a8e932480431039bdb93dffe6813e

# Retry server errors from Maven repositories, as the resolver already does
# for 429 and 503: Maven Central sometimes answers 502 for a moment. As a
# system property, a module's own setting of the same key still wins.
ENV MAVEN_OPTS="-Daether.connector.http.retryHandler.serviceUnavailable=429,500,502,503,504"
