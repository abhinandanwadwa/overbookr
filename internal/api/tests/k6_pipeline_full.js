import http from "k6/http";
import { check, sleep } from "k6";
import exec from "k6/execution";
import { Counter } from "k6/metrics";

export const options = {
  vus: 20,
  duration: "30s",
  thresholds: { checks: ["rate>0.90"] },
};

const BASE_URL = __ENV.BASE_URL || "http://localhost:8080";

// Metrics
const holdsCreated = new Counter("holds_created");
const holdsConflict = new Counter("holds_conflict");
const bookingsCreated = new Counter("bookings_created");
const bookingsConflict = new Counter("bookings_conflict");
const bookingsIdem = new Counter("bookings_idempotent");
const cancels = new Counter("cancels");
const waitlists = new Counter("waitlists");
const unexpectedErrors = new Counter("unexpected_errors");

function postJSON(url, body, headers) {
  return http.post(url, JSON.stringify(body), headers);
}

export function setup() {
  // create admin, login, create event and seats
  const adminEmail = `k6-admin-${Date.now()}@test.local`;
  http.post(`${BASE_URL}/users/register`, JSON.stringify({
    name: "k6-admin", email: adminEmail, password: "password", role: "admin"
  }), { headers: { "Content-Type": "application/json" } });

  let adminToken = null;
  for (let i = 0; i < 3; i++) {
    const res = postJSON(`${BASE_URL}/users/login`, { email: adminEmail, password: "password" }, { headers: { "Content-Type": "application/json" } });
    if (res.status === 200) {
      adminToken = JSON.parse(res.body).token;
      break;
    }
    sleep(0.2);
  }
  if (!adminToken) return { error: "admin_token_missing" };

  const ev = postJSON(`${BASE_URL}/events`, {
    name: "k6-full-event",
    venue: "hall",
    start_time: new Date().toISOString(),
    capacity: 100,
    metadata: {}
  }, { headers: { "Content-Type": "application/json", Authorization: `Bearer ${adminToken}` } });

  if (ev.status !== 201) return { error: "create_event_failed", body: ev.body };

  const eventId = JSON.parse(ev.body).id;
  const seatNos = Array.from({ length: 100 }, (_, i) => `S${i+1}`);

  const seed = postJSON(`${BASE_URL}/events/${eventId}/seats`, { seat_nos: seatNos }, { headers: { "Content-Type": "application/json", Authorization: `Bearer ${adminToken}` }});
  if (seed.status !== 200 && seed.status !== 201) return { error: "seed_failed", body: seed.body };

  // prepare user tokens for VUs
  const users = [];
  for (let i = 0; i < options.vus; i++) {
    const email = `k6-user-${i}-${Date.now()}@test.local`;
    http.post(`${BASE_URL}/users/register`, JSON.stringify({ name: `k6-user-${i}`, email, password: "password", role: "user" }), { headers: { "Content-Type": "application/json" } });
    let token = null;
    for (let r = 0; r < 3; r++) {
      const login = postJSON(`${BASE_URL}/users/login`, { email, password: "password" }, { headers: { "Content-Type": "application/json" }});
      if (login.status === 200) {
        token = JSON.parse(login.body).token;
        break;
      }
      sleep(0.1);
    }
    users.push(token); // may be null; fallback per-VU handled later
  }

  return { eventId, users };
}

const perVUState = {}; // hold per VU sets (waitlist state)
function ensureVUState() {
  const id = exec.vu.idInTest;
  if (!perVUState[id]) perVUState[id] = { joinedWaitlist: false };
  return perVUState[id];
}

function isTransientError(status) {
  return status >= 500 || status === 0;
}

function retryablePost(url, body, headers, maxAttempts=3) {
  for (let attempt = 1; attempt <= maxAttempts; attempt++) {
    const res = http.post(url, JSON.stringify(body), headers);
    if (!isTransientError(res.status)) return res;
    // transient -> backoff
    sleep(0.05 * attempt);
  }
  return { status: 0, body: "" };
}

export default function(data) {
  if (!data || data.error) {
    unexpectedErrors.add(1);
    console.error("setup error:", data ? data : "no data");
    return;
  }

  const vuState = ensureVUState();
  const { eventId } = data;
  const vuIndex = exec.vu.idInTest - 1;
  let token = (Array.isArray(data.users) && data.users[vuIndex]) ? data.users[vuIndex] : null;

  if (!token) {
    // fallback: register/login now
    const email = `k6-fallback-${exec.vu.idInTest}-${Date.now()}@test.local`;
    http.post(`${BASE_URL}/users/register`, JSON.stringify({ name: `k6-fallback-${exec.vu.idInTest}`, email, password: "password", role: "user" }), { headers: { "Content-Type": "application/json" }});
    const login = postJSON(`${BASE_URL}/users/login`, { email, password: "password" }, { headers: { "Content-Type": "application/json" }});
    if (login.status !== 200) {
      unexpectedErrors.add(1);
      console.error("fallback login failed:", login.status, login.body);
      return;
    }
    token = JSON.parse(login.body).token;
  }

  const headers = { "Content-Type": "application/json", Authorization: `Bearer ${token}` };

  // pick random seat
  const seat = `S${Math.floor(Math.random() * 100) + 1}`;

  // 1) try hold (with retry on transient)
  const holdRes = retryablePost(`${BASE_URL}/holds`, { event_id: eventId, seat_nos: [seat] }, { headers });
  if (holdRes.status === 201) {
    holdsCreated.add(1);
    let holdToken = null;
    try { holdToken = JSON.parse(holdRes.body).hold_token; } catch (e) { holdToken = null; }
    if (!holdToken) {
      unexpectedErrors.add(1);
      console.error("hold got 201 but no token:", holdRes.body);
    } else {
      // 2) booking (idempotent header)
      const idemKey = `idem-${exec.vu.idInTest}-${Date.now()}`;
      const bookRes = retryablePost(`${BASE_URL}/bookings`, { event_id: eventId, hold_token: holdToken }, { headers: {...headers, "Idempotency-Key": idemKey }});

      if (bookRes.status === 201) bookingsCreated.add(1);
      else if (bookRes.status === 200) bookingsIdem.add(1);
      else if (bookRes.status === 409) bookingsConflict.add(1);
      else {
        unexpectedErrors.add(1);
        console.error("booking unexpected", bookRes.status, bookRes.body);
      }

      // maybe cancel
      if (Math.random() < 0.10 && bookRes.status === 201) {
        let bookingId = null;
        try { bookingId = JSON.parse(bookRes.body).id; } catch (e) { bookingId = null; }
        if (bookingId) {
          const cancel = http.del(`${BASE_URL}/bookings/${bookingId}`, null, { headers });
          if (cancel.status === 200) cancels.add(1);
          else {
            unexpectedErrors.add(1);
            console.error("cancel failed", cancel.status, cancel.body);
          }
        }
      }
    }
  } else if (holdRes.status === 409) {
    holdsConflict.add(1);
  } else {
    unexpectedErrors.add(1);
    console.error("hold unexpected", holdRes.status, holdRes.body);
  }

  // 3) Waitlist: do only once per VU to avoid duplicate-key spam
  if (!vuState.joinedWaitlist && Math.random() < 0.1) { // 10% chance to attempt
    const wlRes = http.post(`${BASE_URL}/events/${eventId}/waitlist`, JSON.stringify({ requested_seats: 1 }), { headers });
    if (wlRes.status === 202) {
      waitlists.add(1);
      vuState.joinedWaitlist = true;
    } else if (wlRes.status === 409) {
      // already in waitlist (expected) -> mark as joined so we don't spam again
      vuState.joinedWaitlist = true;
    } else if (wlRes.status >= 500) {
      // server trouble -> count but do not flip state (maybe try later)
      unexpectedErrors.add(1);
      console.warn("waitlist failed (server)", wlRes.status, wlRes.body);
    } else {
      unexpectedErrors.add(1);
      console.warn("waitlist unexpected", wlRes.status, wlRes.body);
    }
  }

  check(holdRes, { "hold ok/conflict": (r) => [201, 409].includes(r.status) });

  sleep(Math.random() * 1.2);
}

export function handleSummary(data) {
  console.log("\n==== Custom Test Summary ====");
  const m = data.metrics || {};
  console.log("holds_created:", m.holds_created?.values?.count || 0);
  console.log("holds_conflict:", m.holds_conflict?.values?.count || 0);
  console.log("bookings_created:", m.bookings_created?.values?.count || 0);
  console.log("bookings_idempotent:", m.bookings_idempotent?.values?.count || 0);
  console.log("bookings_conflict:", m.bookings_conflict?.values?.count || 0);
  console.log("cancels:", m.cancels?.values?.count || 0);
  console.log("waitlists:", m.waitlists?.values?.count || 0);
  console.log("unexpected_errors:", m.unexpected_errors?.values?.count || 0);
  console.log("==============================\n");
  return { stdout: JSON.stringify(data) };
}
