FROM alpine:latest@sha256:ab00606a42621fb68f2ed6ad3c88be54397f981a7b70a79db3d1172b11c4367d
COPY . /dir
RUN test $(cat /dir/foo) = foobar
