const http = require("node:http");

const server = http.createServer((_req, res) => {
  res.end("admin\n");
});

server.listen(8080, "0.0.0.0");
