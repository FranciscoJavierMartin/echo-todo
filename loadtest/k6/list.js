// k6 scenario: log in once per VU, then page through the list endpoint with a
// cursor and occasionally create a todo. Thresholds encode the SLO.
//
//   k6 run -e BASE_URL=http://localhost:8080 -e EMAIL=load@example.com \
//          -e PASSWORD='correct horse battery staple' loadtest/k6/list.js
import http from "k6/http";
import { check, sleep } from "k6";

const BASE = __ENV.BASE_URL || "http://localhost:8080";

export const options = {
  scenarios: {
    list: {
      executor: "constant-arrival-rate",
      rate: 200, timeUnit: "1s", duration: "1m",
      preAllocatedVUs: 50, maxVUs: 200,
    },
  },
  thresholds: {
    "http_req_duration{endpoint:list}": ["p(95)<50"],
    http_req_failed: ["rate<0.01"],
  },
};

export function setup() {
  // Register is idempotent enough for a load test: 409 when the user exists.
  http.post(`${BASE}/v1/auth/register`, JSON.stringify({
    email: __ENV.EMAIL, password: __ENV.PASSWORD, display_name: "k6",
  }), { headers: { "Content-Type": "application/json" } });
  const res = http.post(`${BASE}/v1/auth/login`, JSON.stringify({
    email: __ENV.EMAIL, password: __ENV.PASSWORD,
  }), { headers: { "Content-Type": "application/json" } });
  check(res, { "login 200": (r) => r.status === 200 });
  return { token: res.json("access_token") };
}

export default function (data) {
  const headers = { Authorization: `Bearer ${data.token}`, "Content-Type": "application/json" };
  let cursor = "";
  for (let page = 0; page < 3; page++) {
    const q = `limit=20&sort=-priority,due_at&status=open${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`;
    const res = http.get(`${BASE}/v1/todos?${q}`, { headers, tags: { endpoint: "list" } });
    check(res, { "list 200": (r) => r.status === 200 });
    cursor = res.json("next_cursor");
    if (!cursor) break;
  }
  if (Math.random() < 0.05) {
    const res = http.post(`${BASE}/v1/todos`, JSON.stringify({ title: `k6 ${__VU}-${__ITER}`, priority: 1 + (__ITER % 5) }),
      { headers: { ...headers, "Idempotency-Key": `k6-${__VU}-${__ITER}` }, tags: { endpoint: "create" } });
    check(res, { "create 201": (r) => r.status === 201 });
  }
  sleep(0.1);
}
