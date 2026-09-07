// Functional tests of the shipped relay binary, using real TCP/WS/UDP sockets.
import net from 'node:net';
import dgram from 'node:dgram';
import { randomUUID, randomBytes } from 'node:crypto';
import { readFileSync } from 'node:fs';
import assert from 'node:assert/strict';
const host = process.env.RCR_HOST || '127.0.0.1';
const tcpPort = +(process.env.RCR_TCP_PORT || 23417);
const wsPort = +(process.env.RCR_WS_PORT || 23419);
const controlPort = +(process.env.RCR_CONTROL_PORT || 23419);
const token = readFileSync(process.env.RCR_SECRET_FILE, 'utf8').trim();
const delay = ms => new Promise(r => setTimeout(r, ms));
async function until(fn, label, timeout = 5000) {
  const start = Date.now();
  while (!fn()) { if (Date.now() - start > timeout) throw Error(`Timeout: ${label}`); await delay(10); }
}
const udp = dgram.createSocket('udp4');
const replies = new Map();
udp.on('message', data => { const value = JSON.parse(data); replies.set(value.request_id, value); });
async function control(uuid, extra = {}) {
  const value = { token, uuid, expires_in: 30, ...extra };
  await new Promise((resolve, reject) => udp.send(Buffer.from(JSON.stringify(value)), controlPort, host, e => e ? reject(e) : resolve()));
  await delay(100);
}
function request(uuid) {
  const id = Buffer.from(uuid);
  const nested = Buffer.concat([Buffer.from([0x12, id.length]), id]);
  return Buffer.concat([Buffer.from([0x92, 0x01, nested.length]), nested]);
}
const clients = [];
async function connect(kind, uuid) {
  const value = { data: Buffer.alloc(0), closed: false };
  const append = data => { value.data = Buffer.concat([value.data, Buffer.from(data)]); };
  if (kind === 'tcp') {
    const socket = net.connect(tcpPort, host);
    value.socket = socket;
    socket.on('data', append).on('close', () => value.closed = true).on('error', e => value.error = e);
    await new Promise((resolve, reject) => socket.once('connect', resolve).once('error', reject));
    value.send = data => socket.write(data);
    const packet = request(uuid);
    assert.ok(packet.length < 64);
    socket.write(Buffer.concat([Buffer.from([packet.length << 2]), packet]));
    value.dispose = () => socket.destroy();
  } else {
    const socket = new WebSocket(`ws://${host}:${wsPort}/ws/relay`);
    value.socket = socket;
    socket.binaryType = 'arraybuffer';
    socket.addEventListener('message', event => append(event.data));
    socket.addEventListener('close', () => value.closed = true);
    socket.addEventListener('error', event => value.error = event);
    await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }); });
    value.send = data => socket.send(data);
    value.dispose = () => socket.close();
    socket.send(request(uuid));
  }
  clients.push(value);
  return value;
}
async function deny(uuid, kind = 'tcp') {
  const client = await connect(kind, uuid);
  await until(() => client.closed, `deny ${kind}`);
  assert.equal(client.data.length, 0);
}
const results = [];
try {
  await deny(randomUUID());
  await deny(randomUUID(), 'ws');
  results.push('TCP and WebSocket deny without permit');
  const invalid = randomUUID();
  await control(invalid, { token: 'invalid-secret' });
  await deny(invalid);
  results.push('invalid control token rejected');
  const expired = randomUUID();
  await control(expired, { expires_in: 1 });
  await delay(1100);
  await deny(expired);
  results.push('expired permit rejected');
  for (const pair of [['tcp', 'tcp'], ['ws', 'ws'], ['tcp', 'ws']]) {
    const uuid = randomUUID();
    await control(uuid);
    const a = await connect(pair[0], uuid), b = await connect(pair[1], uuid);
    const forward = randomBytes(131071), backward = randomBytes(65537);
    a.send(forward);
    await until(() => b.data.length >= forward.length, `${pair} forward`);
    assert.deepEqual(b.data, forward);
    b.send(backward);
    await until(() => a.data.length >= backward.length, `${pair} backward`);
    assert.deepEqual(a.data, backward);
    await deny(uuid);
    const request_id = randomUUID();
    await control(uuid, { action: 'terminate', request_id });
    await until(() => replies.has(request_id), 'terminate ack');
    assert.equal(replies.get(request_id).status, 'terminated');
    await until(() => a.closed && b.closed, 'both endpoints terminated');
    results.push(`${pair.join('/')} bidirectional byte equality, third peer denied, terminate acknowledged and both peers closed`);
  }
  if (process.env.RCR_API_URL) {
    let registered = false;
    for (let attempt = 0; attempt < 20; attempt++) {
      const response = await fetch(`${process.env.RCR_API_URL}/internal/v1/auth/snapshot`, {
        headers: { 'X-RDS-Internal-Token': token }, signal: AbortSignal.timeout(5000)
      });
      assert.equal(response.status, 200);
      const snapshot = await response.json();
      registered = snapshot.relay_servers.some(relay => relay.id === process.env.RCR_RELAY_ID);
      if (registered) break;
      await delay(500);
    }
    assert.ok(registered, 'relay telemetry registered in central API');
    results.push('relay telemetry registered in isolated central API');
  }
  console.log(JSON.stringify({ passed: true, results }, null, 2));
} finally {
  for (const client of clients) client.dispose();
  udp.close();
}
