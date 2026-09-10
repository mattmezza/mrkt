#!/usr/bin/env node

import {createHmac, timingSafeEqual} from "node:crypto";
import {appendFileSync, existsSync, readFileSync} from "node:fs";
import {createServer} from "node:http";
import {tmpdir} from "node:os";
import {join} from "node:path";

const secret = process.env.MRKT_WEBHOOK_SECRET;
const journal = process.env.MRKT_WEBHOOK_JOURNAL || join(tmpdir(), "mrkt-webhook-events.jsonl");
const maxAge = 600;
const seen = new Set();
if (existsSync(journal)) for (const line of readFileSync(journal, "utf8").split("\n")) {
  try { const row = JSON.parse(line); if (row.id) seen.add(row.id); } catch {}
}
function valid(body, timestamp, header, now = Date.now()) {
  if (!/^\d+$/.test(timestamp) || !header.startsWith("v1=")) return false;
  const seconds = Number(timestamp);
  if (seconds > Math.floor(now / 1000) + 60 || Math.floor(now / 1000) - seconds > maxAge) return false;
  const expected = createHmac("sha256", secret).update(`${timestamp}.`).update(body).digest();
  let actual; try { actual = Buffer.from(header.slice(3), "hex"); } catch { return false; }
  return actual.length === expected.length && timingSafeEqual(actual, expected);
}
function accept(body, headers) {
  if (!valid(body, headers.timestamp || "", headers.signature || "")) return 401;
  let event; try { event = JSON.parse(body); } catch { return 400; }
  if (!event.id || headers.eventID !== event.id) return 400;
  if (seen.has(event.id)) return 204;
  appendFileSync(journal, `${JSON.stringify({id: event.id, received_at: new Date().toISOString()})}\n`, {mode: 0o600, flush: true});
  seen.add(event.id);
  return 204;
}
if (process.argv.includes("--self-test")) {
  if (!secret) throw new Error("MRKT_WEBHOOK_SECRET is required");
  const id = `synthetic-event-${Date.now()}`;
  const before = seen.size;
  const body = Buffer.from(JSON.stringify({id, type: "synthetic.test", data: {}}));
  const timestamp = String(Math.floor(Date.now() / 1000));
  const signature = `v1=${createHmac("sha256", secret).update(`${timestamp}.`).update(body).digest("hex")}`;
  const headers = {timestamp, signature, eventID: id};
  if (accept(body, headers) !== 204 || accept(body, headers) !== 204 || seen.size !== before + 1) throw new Error("self-test failed");
  process.stdout.write("webhook signature and durable deduplication self-test passed\n");
} else {
  if (!secret) throw new Error("MRKT_WEBHOOK_SECRET is required");
  createServer((request, response) => {
    const chunks = []; let size = 0; let tooLarge = false;
    request.on("data", chunk => {
      size += chunk.length;
      if (size > 1 << 20) tooLarge = true;
      else chunks.push(chunk);
    });
    request.on("end", () => {
      if (tooLarge) { response.writeHead(413).end(); return; }
      const body = Buffer.concat(chunks);
      const status = request.method === "POST" ? accept(body, {
        timestamp: request.headers["x-mrkt-timestamp"], signature: request.headers["x-mrkt-signature"], eventID: request.headers["x-mrkt-event-id"],
      }) : 405;
      response.writeHead(status).end();
    });
  }).listen(Number(process.env.PORT || 8787), "127.0.0.1", () => process.stdout.write("listening on http://127.0.0.1:8787\n"));
}
