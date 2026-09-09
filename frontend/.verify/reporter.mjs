import { createServer } from "node:http";
const server = createServer((req, res) => {
  if (req.method !== "POST") { res.writeHead(204); return res.end(); }
  let body = "";
  req.on("data", (c) => (body += c));
  req.on("end", () => {
    res.writeHead(200, { "access-control-allow-origin": "*" });
    res.end("ok");
    console.log(body);
    let failed = true;
    try { failed = JSON.parse(body).some((r) => !r.pass); } catch {}
    server.close();
    process.exit(failed ? 1 : 0);
  });
});
server.listen(7788, "127.0.0.1", () => console.error("reporter on 7788"));
setTimeout(() => { console.error("TIMEOUT: harness never reported"); process.exit(2); }, 120000);
