// k6 load test — measures throughput and latency across all 6 framework variants.
// Run: k6 run k6/load-test.js
//
// What this tests: raw HTTP performance under 50 VUs for 30s.
// Expected result: all 6 should be similar — router throughput is NOT the bottleneck.
// The purpose is to confirm there's no significant performance difference
// so the framework decision can rest on developer ergonomics, not benchmarks.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';

const errorRate = new Rate('errors');

export const options = {
  scenarios: {
    'echo-standard': {
      executor: 'constant-vus',
      vus: 50,
      duration: '30s',
      env: { PORT: '9001', VARIANT: 'echo-standard' },
      tags: { variant: 'echo-standard' },
    },
    'echo-strict': {
      executor: 'constant-vus',
      vus: 50,
      duration: '30s',
      startTime: '35s',
      env: { PORT: '9002', VARIANT: 'echo-strict' },
      tags: { variant: 'echo-strict' },
    },
    'chi-standard': {
      executor: 'constant-vus',
      vus: 50,
      duration: '30s',
      startTime: '70s',
      env: { PORT: '9003', VARIANT: 'chi-standard' },
      tags: { variant: 'chi-standard' },
    },
    'chi-strict': {
      executor: 'constant-vus',
      vus: 50,
      duration: '30s',
      startTime: '105s',
      env: { PORT: '9004', VARIANT: 'chi-strict' },
      tags: { variant: 'chi-strict' },
    },
    'gin-standard': {
      executor: 'constant-vus',
      vus: 50,
      duration: '30s',
      startTime: '140s',
      env: { PORT: '9005', VARIANT: 'gin-standard' },
      tags: { variant: 'gin-standard' },
    },
    'gin-strict': {
      executor: 'constant-vus',
      vus: 50,
      duration: '30s',
      startTime: '175s',
      env: { PORT: '9006', VARIANT: 'gin-strict' },
      tags: { variant: 'gin-strict' },
    },
  },
  thresholds: {
    'http_req_duration{variant:echo-standard}': ['p(99)<50'],
    'http_req_duration{variant:echo-strict}':   ['p(99)<50'],
    'http_req_duration{variant:chi-standard}':  ['p(99)<50'],
    'http_req_duration{variant:chi-strict}':    ['p(99)<50'],
    'http_req_duration{variant:gin-standard}':  ['p(99)<50'],
    'http_req_duration{variant:gin-strict}':    ['p(99)<50'],
  },
};

// Seed data — pre-create instances so GET/DELETE have data to work with.
const instances = Array.from({ length: 20 }, (_, i) => `load-test-${i}`);
let seeded = false;

export function setup() {
  // Seed all 6 variants with the same instances.
  const ports = [9001, 9002, 9003, 9004, 9005, 9006];
  for (const port of ports) {
    for (const name of instances) {
      http.post(`http://localhost:${port}/compute`, JSON.stringify({
        name: name,
        tenantId: 'tenant-load-test',
        provider: 'gcp',
        size: 'medium',
      }), { headers: { 'Content-Type': 'application/json' } });
    }
  }
}

export default function () {
  const port = __ENV.PORT;
  const base = `http://localhost:${port}`;
  const name = instances[Math.floor(Math.random() * instances.length)];

  // Distribute requests across all 4 endpoints.
  const scenario = Math.random();

  if (scenario < 0.4) {
    // POST /compute — most interesting for provisioning simulation
    const payload = JSON.stringify({
      name: `perf-${Date.now()}-${Math.random().toString(36).slice(2)}`,
      tenantId: 'tenant-load-test',
      provider: 'gcp',
      size: 'medium',
    });
    const res = http.post(`${base}/compute`, payload, {
      headers: { 'Content-Type': 'application/json' },
    });
    check(res, { 'POST /compute 202': (r) => r.status === 202 });
    errorRate.add(res.status !== 202);

  } else if (scenario < 0.6) {
    // GET /compute?tenantId=... — list
    const res = http.get(`${base}/compute?tenantId=tenant-load-test`);
    check(res, { 'GET /compute 200': (r) => r.status === 200 });
    errorRate.add(res.status !== 200);

  } else if (scenario < 0.8) {
    // GET /compute/:name — single instance
    const res = http.get(`${base}/compute/${name}`);
    check(res, { 'GET /compute/:name 200 or 404': (r) => r.status === 200 || r.status === 404 });
    errorRate.add(res.status !== 200 && res.status !== 404);

  } else {
    // DELETE /compute/:name
    const res = http.del(`${base}/compute/${name}`);
    check(res, { 'DELETE /compute/:name 204 or 404 or 409': (r) => [204, 404, 409].includes(r.status) });
    errorRate.add(![204, 404, 409].includes(res.status));
  }
}
