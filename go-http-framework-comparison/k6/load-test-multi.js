// load-test-multi.js — runs all 6 variants simultaneously (not sequentially)
// to eliminate ordering bias. Run this multiple times independently to see
// if rankings are consistent or noise.
//
// Usage:
//   k6 run k6/load-test-multi.js                    # single run
//   for i in {1..5}; do k6 run k6/load-test-multi.js 2>&1 | grep "p(99)"; done
//
// All 6 variants run at the same time under the same system conditions.
// This removes the "runs last = warmer store" bias from load-test.js.

import http from 'k6/http';
import { check } from 'k6';
import { Rate } from 'k6/metrics';

const errorRate = new Rate('errors');

export const options = {
  scenarios: {
    'echo-standard': {
      executor: 'constant-vus',
      vus: 50,
      duration: '30s',
      env: { PORT: '9001' },
      tags: { variant: 'echo-standard' },
    },
    'echo-strict': {
      executor: 'constant-vus',
      vus: 50,
      duration: '30s',
      env: { PORT: '9002' },
      tags: { variant: 'echo-strict' },
    },
    'chi-standard': {
      executor: 'constant-vus',
      vus: 50,
      duration: '30s',
      env: { PORT: '9003' },
      tags: { variant: 'chi-standard' },
    },
    'chi-strict': {
      executor: 'constant-vus',
      vus: 50,
      duration: '30s',
      env: { PORT: '9004' },
      tags: { variant: 'chi-strict' },
    },
    'gin-standard': {
      executor: 'constant-vus',
      vus: 50,
      duration: '30s',
      env: { PORT: '9005' },
      tags: { variant: 'gin-standard' },
    },
    'gin-strict': {
      executor: 'constant-vus',
      vus: 50,
      duration: '30s',
      env: { PORT: '9006' },
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

const instances = Array.from({ length: 20 }, (_, i) => `load-test-${i}`);

export function setup() {
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
  const scenario = Math.random();

  if (scenario < 0.4) {
    const res = http.post(`${base}/compute`, JSON.stringify({
      name: `perf-${Date.now()}-${Math.random().toString(36).slice(2)}`,
      tenantId: 'tenant-load-test',
      provider: 'gcp',
      size: 'medium',
    }), { headers: { 'Content-Type': 'application/json' } });
    check(res, { 'POST 202': (r) => r.status === 202 });
    errorRate.add(res.status !== 202);
  } else if (scenario < 0.6) {
    const res = http.get(`${base}/compute?tenantId=tenant-load-test`);
    check(res, { 'GET list 200': (r) => r.status === 200 });
    errorRate.add(res.status !== 200);
  } else if (scenario < 0.8) {
    const res = http.get(`${base}/compute/${name}`);
    check(res, { 'GET one 200/404': (r) => r.status === 200 || r.status === 404 });
    errorRate.add(res.status !== 200 && res.status !== 404);
  } else {
    const res = http.del(`${base}/compute/${name}`);
    check(res, { 'DELETE 204/404/409': (r) => [204, 404, 409].includes(r.status) });
    errorRate.add(![204, 404, 409].includes(res.status));
  }
}
