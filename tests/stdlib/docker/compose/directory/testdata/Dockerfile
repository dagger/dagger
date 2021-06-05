FROM node:12-alpine

WORKDIR /app

COPY package.json package.json

RUN npm install

COPY . .

ENV PORT=8080

EXPOSE 8080

CMD ["npm", "start"]