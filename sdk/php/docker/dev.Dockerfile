FROM golang:1.22-alpine3.19 AS golang-base
FROM php:8.2-zts-bookworm AS php-base
FROM composer/composer:2-bin AS composer_upstream
FROM docker:20.10.17-cli-alpine3.16 AS docker-cli

# Dagger Engine Dev
FROM golang-base AS dev-dagger-engine
WORKDIR /srv
RUN apk add --no-cache file git bash
COPY --link --from=docker-cli /usr/local/bin/docker /usr/local/bin/docker
ARG UID=1000
RUN adduser -u $UID dagger -D && mkdir /home/dagger/.cache && chown -R $UID /srv /home/dagger/.cache
USER dagger
VOLUME /home/dagger/.cache

# Base PHP image with docker cli
FROM php-base AS base

COPY --link --from=docker-cli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=composer_upstream --link /composer /usr/bin/composer
COPY --from=mlocati/php-extension-installer /usr/bin/install-php-extensions /usr/local/bin/

RUN curl -1sLf 'https://dl.cloudsmith.io/public/symfony/stable/setup.deb.sh' | bash

RUN apt-get update && \
    apt-get -y --no-install-recommends install \
    git \
    unzip \
    socat \
    && \
    apt-get clean

RUN set -eux; \
    install-php-extensions \
    apcu \
    intl \
    opcache \
    zip \
    ;

ARG UID=1000
ARG GID=100
RUN usermod -u $UID -g $GID www-data && chown -R www-data:www-data /var/www
USER www-data

FROM base AS dev
RUN composer global require vimeo/psalm friendsofphp/php-cs-fixer phpunit/phpunit
RUN echo "export PATH=$PATH:/var/www/.composer/vendor/bin/" >> /var/www/.bashrc
