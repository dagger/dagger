const http = require("node:http");

if (process.argv.includes("--build")) {
  process.stdout.write("build ok\n");
  process.exit(0);
}

const server = http.createServer((_req, res) => {
  res.end("web\n");
});

server.listen(3000, "0.0.0.0");
