# https://github.com/docker/for-win/issues/5254

FROM hello-world

ENV A path
ENV B another\\path
ENV C trailing\\backslash\\
ENV D This should not be appended to C
ENV E hello\
\
world
ENV F hello\
 \
world
ENV G hello \
\
world
