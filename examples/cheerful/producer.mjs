#!/usr/bin/env node

const base = (process.env.MRKT_URL || "http://127.0.0.1:8080").replace(/\/$/, "");
const token = process.env.MRKT_TOKEN;
if (!token) throw new Error("MRKT_TOKEN is required");
const key = process.env.EVENT_KEY || `article-demo-${Date.now()}`;
const body = {key, type: "article.published", payload: {slug: "a-synthetic-card", synthetic: true}};
const response = await fetch(`${base}/api/v1/projects/${encodeURIComponent(process.env.MRKT_PROJECT || "cheerful")}/events`, {
  method: "POST", headers: {authorization: `Bearer ${token}`, "content-type": "application/json", "idempotency-key": key}, body: JSON.stringify(body),
});
if (!response.ok) throw new Error(`mrkt returned ${response.status}: ${await response.text()}`);
process.stdout.write(`${await response.text()}\n`);
