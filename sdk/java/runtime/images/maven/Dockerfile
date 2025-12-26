FROM maven:3.9.9-eclipse-temurin-21-alpine@sha256:4cbb8bf76c46b97e028998f2486ed014759a8e932480431039bdb93dffe6813e

# Allow Java to use the system-managed CA store:
RUN apk add --no-cache java-cacerts
ENV MAVEN_OPTS=-Djavax.net.ssl.trustStore=/etc/ssl/certs/java/cacerts
