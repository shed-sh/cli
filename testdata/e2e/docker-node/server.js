const http = require("node:http");

const message = "shed-e2e-v1";
const port = Number(process.env.PORT || 8080);

http.createServer((_request, response) => {
  response.writeHead(200, { "content-type": "text/plain" });
  response.end(message + "\n");
}).listen(port, "0.0.0.0");
