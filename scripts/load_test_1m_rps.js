import http from "k6/http";
import { check } from "k6";
import { Rate, Trend } from "k6/metrics";

const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";
const TARGET_RATE = parseInt(__ENV.RATE) || 100000;
const TEST_DURATION = __ENV.DURATION || "60s";
const POOL_SIZE = parseInt(__ENV.POOL_SIZE) || "10000";
const SHORT_CODE = __ENV.SHORT_CODE || "";
const CREATE_RATIO = parseFloat(__ENV.CREATE_RATIO || "0.3");

const errorRate = new Rate("errors");
const latency = new Trend("latency_ms", true);

export const options = {
  scenarios: {
    max_throughput: {
      executor: "constant-arrival-rate",
      rate: TARGET_RATE,
      timeUnit: "1s",
      duration: TEST_DURATION,
      preAllocatedVUs: 2000,
      maxVUs: 50000,
    },
  },
  thresholds: {
    errors: ["rate<0.99"],
    http_req_duration: ["p(95)<10000"],
  },
};

function makeid() {
  const c = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789";
  let s = "";
  for (let i = 0; i < 12; i++) s += c[Math.floor(Math.random() * c.length)];
  return s;
}

export function setup() {
  if (SHORT_CODE) {
    console.log(`Using SHORT_CODE: ${SHORT_CODE}`);
    return { codes: [SHORT_CODE], token: "", mode: "single" };
  }

  const email = `pool_${Date.now()}@loadtest.local`;
  const password = "Pooltest123!";

  http.post(`${BASE_URL}/api/auth/register`,
    JSON.stringify({ email, password }),
    { headers: { "Content-Type": "application/json" }, timeout: "10s" }
  );

  const login = http.post(`${BASE_URL}/api/auth/login`,
    JSON.stringify({ email, password }),
    { headers: { "Content-Type": "application/json" }, timeout: "10s" }
  );

  let token = "";
  if (login.status === 200) {
    try { token = JSON.parse(login.body).token; } catch (_) {}
  }

  if (!token) {
    console.warn("Login failed!");
    return { codes: ["loadtest"], token: "", mode: "fallback" };
  }

  console.log("Creating URL pool (may take a moment)...");
  const pool = [];
  let rateLimited = false;

  for (let attempt = 0; attempt < POOL_SIZE && !rateLimited; attempt++) {
    const res = http.post(`${BASE_URL}/api/shorten`,
      JSON.stringify({ url: `https://httpbin.org/get?t=${Date.now()}${makeid()}` }),
      {
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        timeout: "10s",
      }
    );

    if (res.status === 201 || res.status === 200) {
      try {
        const body = JSON.parse(res.body);
        const code = body.short_code || body.shortCode || body.code;
        if (code) pool.push(code);
      } catch (_) {}
    } else if (res.status === 429) {
      rateLimited = true;
    }

    if (attempt > 0 && attempt % 50 === 0) {
      console.log(`  created ${pool.length}/${attempt + 1} codes so far...`);
    }
  }

  if (rateLimited) {
    console.warn(`⚠️  Rate limited after ${pool.length} codes (gateway: 10 shortens/60s/IP)`);
    console.warn(`   Increase limit: set SHORTEN_RATE_LIMIT=10000 in gateway & restart`);
  }

  console.log(`Pool: ${pool.length} codes, mode=${pool.length > 5 ? "mixed" : "tiny"}, create_ratio=${CREATE_RATIO}`);
  return { codes: pool.length > 1 ? pool : ["loadtest"], token, mode: pool.length > 5 ? "mixed" : "tiny" };
}

export default function (data) {
  const codes = data.codes;
  const token = data.token;
  const isCreate = token && Math.random() < CREATE_RATIO;

  if (isCreate) {
    const res = http.post(`${BASE_URL}/api/shorten`,
      JSON.stringify({ url: `https://httpbin.org/get?t=${Date.now()}${makeid()}` }),
      {
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        timeout: "30s",
        tags: { name: "shorten" },
      }
    );
    latency.add(res.timings.duration);
    errorRate.add(res.status >= 500 ? 1 : 0);
    check(res, { "create ok": (r) => r.status < 500 });
    return;
  }

  const code = codes[Math.floor(Math.random() * codes.length)];
  const res = http.get(`${BASE_URL}/r/${code}`, {
    redirects: 0,
    timeout: "30s",
    tags: { name: "redirect" },
  });

  latency.add(res.timings.duration);
  const isErr = res.status >= 500 && res.status !== 503;
  errorRate.add(isErr ? 1 : 0);
  check(res, { "ok or cb": (r) => (r.status >= 200 && r.status < 500) || r.status === 503 });
}

export function handleSummary(data) {
  const total = data.metrics.http_reqs?.values?.count || 0;
  const dur = data.state.testRunDurationMs / 1000;
  const rps = total / dur;
  const p95 = data.metrics.http_req_duration?.values?.["p(95)"] || 0;
  const p99 = data.metrics.http_req_duration?.values?.["p(99)"] || 0;
  const errs = data.metrics.errors?.values?.rate || 0;
  const out = `[instance] total=${total} dur=${dur.toFixed(1)}s rps=${rps.toFixed(0)} p95=${p95.toFixed(0)}ms p99=${p99.toFixed(0)}ms err=${(errs * 100).toFixed(2)}%`;
  return { stdout: out };
}
