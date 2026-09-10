#!/usr/bin/env node

const base = (process.env.MRKT_URL || "http://127.0.0.1:8080").replace(/\/$/, "");
const token = process.env.MRKT_TOKEN;
if (!token) throw new Error("MRKT_TOKEN is required");
const contactID = process.env.MRKT_CONTACT_ID;
if (!contactID) throw new Error("MRKT_CONTACT_ID is required for this contact event");
const key = process.env.EVENT_KEY || `booking-demo-${Date.now()}`;
const body = {key, type: "booking.completed", contact_id: contactID, payload: {property: "alpine-demo", synthetic: true}};
const response = await fetch(`${base}/api/v1/projects/${encodeURIComponent(process.env.MRKT_PROJECT || "flowrent")}/events`, {
  method: "POST", headers: {authorization: `Bearer ${token}`, "content-type": "application/json", "idempotency-key": key}, body: JSON.stringify(body),
});
if (!response.ok) throw new Error(`mrkt returned ${response.status}: ${await response.text()}`);
process.stdout.write(`${await response.text()}\n`);
