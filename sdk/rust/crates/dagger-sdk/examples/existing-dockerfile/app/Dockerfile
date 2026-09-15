FROM node:16 as builder

COPY . /src

WORKDIR /src

RUN npm install

RUN npm test -- --watchAll=false

RUN npm run build

FROM nginx

COPY --from=builder /src/build /usr/share/nginx/html
