const http = require('node:http');

const port = Number(process.env.PORT || 8080);
const server = http.createServer((_request, response) => {
  response.writeHead(200, { 'content-type': 'text/plain' });
  response.end('shed fixture\n');
});

server.listen(port, '0.0.0.0');
