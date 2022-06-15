FROM python:3.9 as base
ENV VIRTUAL_ENV=/opt/env
ENV PATH="$VIRTUAL_ENV/bin:$PATH"

### "build" base image ###
FROM base as build
# install build dependencies
RUN pip install --no-cache-dir --user pipx
RUN pipx install poetry

### "run" base image ###
FROM base as run
# install run dependencies
COPY ./gunicorn.py /etc/
EXPOSE 5000
WORKDIR /app
CMD ["/start"]
