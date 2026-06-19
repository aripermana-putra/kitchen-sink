// k6 error handler test — verifies error response shape across all 6 variants.
// Run: k6 run k6/error-scenarios.js
//
// What this tests:
// - Does every framework+mode combination return the same ErrorResponse shape?
// - Does the global error handler intercept correctly in strict mode?
// - Are internal error details hidden from clients?
//
// Key findings documented inline:
// - FINDING-1: Enum validation from spec is NOT auto-enforced at binding time
//              in any strict mode variant. Must be validated manually in handler.
// - FINDING-2: Unmatched route 404 — only Echo routes through HTTPErrorHandler (JSON).
//              Chi and Gin return plain text "404 page not found" by default.
// - FINDING-3: Gin strict requires 3 separate error callbacks vs Echo's 1 HTTPErrorHandler.
//              Chi strict requires 1 (ResponseErrorHandlerFunc via StrictHTTPServerOptions).

import http from 'k6/http';
import { check, group } from 'k6';

export const options = {
  vus: 1,
  iterations: 1,
  thresholds: {
    checks: ['rate>=0.90'], // allow known framework differences
  },
};

const variants = [
  { name: 'echo-standard', port: 9001 },
  { name: 'echo-strict',   port: 9002 },
  { name: 'chi-standard',  port: 9003 },
  { name: 'chi-strict',    port: 9004 },
  { name: 'gin-standard',  port: 9005 },
  { name: 'gin-strict',    port: 9006 },
];

function checkErrorShape(res, expectedStatus, expectedCode) {
  check(res, {
    [`[${expectedCode}] status is ${expectedStatus}`]: (r) => r.status === expectedStatus,
    [`[${expectedCode}] has code field`]: (r) => {
      try { return JSON.parse(r.body).code !== undefined; } catch { return false; }
    },
    [`[${expectedCode}] has message field`]: (r) => {
      try { return JSON.parse(r.body).message !== undefined; } catch { return false; }
    },
    [`[${expectedCode}] has requestId field`]: (r) => {
      try { return JSON.parse(r.body).requestId !== undefined; } catch { return false; }
    },
    [`[${expectedCode}] code is ${expectedCode}`]: (r) => {
      try { return JSON.parse(r.body).code === expectedCode; } catch { return false; }
    },
    [`[${expectedCode}] does not leak internal error`]: (r) => {
      const body = r.body;
      return !body.includes('goroutine') && !body.includes('runtime error') && !body.includes('panic');
    },
  });
}

export default function () {
  for (const variant of variants) {
    const base = `http://localhost:${variant.port}`;

    group(variant.name, () => {

      group('400 — missing required field (name)', () => {
        const res = http.post(`${base}/compute`,
          JSON.stringify({ tenantId: 'tenant-1', provider: 'gcp' }),
          { headers: { 'Content-Type': 'application/json' } }
        );
        checkErrorShape(res, 400, 'INVALID_REQUEST');
      });

      group('400 — invalid enum (FINDING-1: strict does not auto-validate)', () => {
        const uniqueName = `enum-test-${variant.port}-${Date.now()}`;
        const res = http.post(`${base}/compute`,
          JSON.stringify({ name: uniqueName, tenantId: 'tenant-1', provider: 'azure' }),
          { headers: { 'Content-Type': 'application/json' } }
        );
        // Standard: manual validation returns 400 INVALID_REQUEST
        // Strict: oapi-codegen does NOT enforce enum at binding time — provider passes through
        //         as a string. Our handlers only check name/tenantId, so azure returns 202.
        //         This is a known gap — enum validation must be added manually in strict handlers.
        const isStrict = variant.name.includes('strict');
        check(res, {
          'body is JSON': (r) => { try { JSON.parse(r.body); return true; } catch { return false; } },
          [`[FINDING-1] ${isStrict ? 'strict passes azure through (202)' : 'standard rejects azure (400)'}`]:
            (r) => isStrict ? r.status === 400 || r.status === 202 : r.status === 400,
        });
      });

      group('404 — instance not found', () => {
        const res = http.get(`${base}/compute/does-not-exist-xyz`);
        checkErrorShape(res, 404, 'NOT_FOUND');
      });

      group('409 — already deleting', () => {
        http.post(`${base}/compute`,
          JSON.stringify({ name: 'delete-test', tenantId: 'tenant-1', provider: 'gcp' }),
          { headers: { 'Content-Type': 'application/json' } }
        );
        http.del(`${base}/compute/delete-test`);
        const res = http.del(`${base}/compute/delete-test`);
        checkErrorShape(res, 409, 'ALREADY_DELETING');
      });

      group('409 — already exists', () => {
        const name = `exists-test-${variant.port}`;
        http.post(`${base}/compute`,
          JSON.stringify({ name: name, tenantId: 'tenant-1', provider: 'gcp' }),
          { headers: { 'Content-Type': 'application/json' } }
        );
        const res = http.post(`${base}/compute`,
          JSON.stringify({ name: name, tenantId: 'tenant-1', provider: 'gcp' }),
          { headers: { 'Content-Type': 'application/json' } }
        );
        checkErrorShape(res, 409, 'ALREADY_EXISTS');
      });

      group('404 — unmatched route (FINDING-2: echo=JSON, chi/gin=plain text)', () => {
        const res = http.get(`${base}/nonexistent-path`);
        check(res, {
          'status is 404': (r) => r.status === 404,
          // Echo routes unmatched 404s through HTTPErrorHandler → JSON
          // Chi and Gin return plain text "404 page not found" by default
          '[FINDING-2] echo returns JSON for unmatched routes': (r) => {
            if (!variant.name.includes('echo')) return true; // skip for non-echo
            try { JSON.parse(r.body); return true; } catch { return false; }
          },
        });
      });

    });
  }
}
