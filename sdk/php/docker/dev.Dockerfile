FROM php:8.2-zts-bookworm as php-base
FROM composer/composer:2-bin AS composer_upstream
FROM docker:20.10.17-cli-alpine3.16 as docker-cli

# Daggger CLI
FROM php-base as dagger-base
ARG DAGGER_VERSION=0.9.3

RUN apt-get update && \
    apt-get -y --no-install-recommends install \
    curl \
    && \
    apt-get clean

RUN curl -L https://dl.dagger.io/dagger/install.sh | DAGGER_VERSION=$DAGGER_VERSION sh


# PHP with docker and dagger CLIs
FROM php-base as php-dagger

COPY --link --from=docker-cli /usr/local/bin/docker /usr/local/bin/docker
COPY --link --from=dagger-base /bin/dagger /usr/local/bin/dagger


# Base image
FROM php-dagger as base

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
RUN mkdir /var/www/dagger && usermod -u $UID -g $GID www-data && chown -R www-data:www-data /var/www

USER www-data
VOLUME /var/www/dagger/

FROM base as dev
RUN composer global require vimeo/psalm friendsofphp/php-cs-fixer phpunit/phpunit
RUN echo "export PATH=$PATH:/var/www/.composer/vendor/bin/" >> /var/www/.bashrc
