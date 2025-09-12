import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Rate } from 'k6/metrics';

// ---------------- CONFIG ----------------
export const options = {
  scenarios: {
    default: {
      executor: 'ramping-vus',
      stages: [
        { duration: '10s', target: 10 },
        { duration: '20s', target: 30 },
        { duration: '30s', target: 50 },
        { duration: '20s', target: 10 },
      ],
      gracefulRampDown: '10s',
    },
  },
  thresholds: {
    'checks': ['rate>0.90'],
  },
};

const BASE_URL = (__ENV.BASE_URL || 'http://localhost:8080').replace(/\/+$/, '');

// --- metrics ---
const holdsCreated = new Counter('holds_created');
const holdsConflict = new Counter('holds_conflict');
const bookingsCreated = new Counter('bookings_created');
const bookingsIdem = new Counter('bookings_idempotent');
const bookingsConflict = new Counter('bookings_conflict');
const cancels = new Counter('cancels');
const waitlists = new Counter('waitlists');
const waitlistsConflict = new Counter('waitlists_conflict');
const unexpectedErrors = new Counter('unexpected_errors');
const http401 = new Counter('http_401');
const http500 = new Counter('http_500');

const successRate = new Rate('checks');

// ---------------- SETUP: create admin, event, seats, users ----------------
export function setup() {
  console.log('SETUP: base url =', BASE_URL);

  // 1) create admin user (register + login)
  const adminName = `k6-admin-${Date.now()}`;
  const adminEmail = `${adminName}@test.local`;
  const adminPass = 'test-password';

  let res = http.post(`${BASE_URL}/users/register`, JSON.stringify({
    name: adminName,
    email: adminEmail,
    password: adminPass,
    role: 'admin',
  }), { headers: { 'Content-Type': 'application/json' } });

  if (res.status !== 201 && res.status !== 400) {
    console.error('SETUP: admin register failed', res.status, res.body);
    throw new Error('admin register failed');
  }

  res = http.post(`${BASE_URL}/users/login`, JSON.stringify({
    email: adminEmail,
    password: adminPass,
  }), { headers: { 'Content-Type': 'application/json' } });

  if (res.status !== 200) {
    console.error('SETUP: admin login failed', res.status, res.body);
    throw new Error('admin login failed');
  }
  const adminToken = JSON.parse(res.body).token;
  console.log('SETUP: admin token OK');

  const authHdr = { headers: { Authorization: `Bearer ${adminToken}`, 'Content-Type': 'application/json' }};

  // 2) create event
  const eventName = `k6-event-${Date.now()}`;
  const startTime = new Date(Date.now() + 3600 * 1000).toISOString(); // 1 hour from now
  res = http.post(`${BASE_URL}/events`, JSON.stringify({
    name: eventName,
    venue: 'k6-venue',
    start_time: startTime,
    capacity: 100,
    metadata: {},
  }), authHdr);

  if (res.status !== 201) {
    console.error('SETUP: create event failed', res.status, res.body);
    throw new Error('create event failed');
  }
  const event = JSON.parse(res.body);
  const eventId = event.id;
  console.log('SETUP: created event', eventId);

  // 3) seed seats S1..S100 using bulk create endpoint
  const seatNos = Array.from({ length: 100 }, (_, i) => `S${i+1}`);
  res = http.post(`${BASE_URL}/events/${eventId}/seats`, JSON.stringify({ seat_nos: seatNos }), authHdr);
  if (res.status !== 201) {
    console.error('SETUP: seed seats failed', res.status, res.body);
    throw new Error('seed seats failed');
  }
  console.log('SETUP: seeded seats 1..100');

  // 4) create N test users (register + login) and return tokens
  const numUsers = Number(__ENV.TEST_USERS) || 40;
  const userTokens = [];
  for (let i = 0; i < numUsers; i++) {
    const name = `k6-user-${i}-${Date.now()}`;
    const email = `${name}@test.local`;
    const pass = 'testpassword';
    let r = http.post(`${BASE_URL}/users/register`, JSON.stringify({
      name, email, password: pass, role: 'user',
    }), { headers: { 'Content-Type': 'application/json' }});

    if (r.status !== 201 && r.status !== 400) {
      console.error('SETUP: user register failed', r.status, r.body);
      throw new Error('user register failed');
    }

    r = http.post(`${BASE_URL}/users/login`, JSON.stringify({
      email, password: pass,
    }), { headers: { 'Content-Type': 'application/json' }});

    if (r.status !== 200) {
      console.error('SETUP: user login failed', r.status, r.body);
      throw new Error('user login failed');
    }
    const token = JSON.parse(r.body).token;
    userTokens.push(token);
  }
  console.log(`SETUP: created ${userTokens.length} test users`);

  // Return data for VUs
  return {
    eventId,
    adminToken,
    userTokens,
    seatNos,
  };
}

// ---------------- per-VU helper to pick a token ----------------
function pickToken(setupData) {
  // Round-robin: use __VU to pick a token so multiple VUs use different users
  const tokens = setupData.userTokens;
  if (!tokens || tokens.length === 0) {
    throw new Error('no user tokens in setup data');
  }
  const idx = (__VU - 1) % tokens.length; // __VU provided by k6 runtime
  return tokens[idx];
}

// ---------------- main test flow ----------------
export default function (setupData) {
  // pick token for this VU
  const token = pickToken(setupData);
  const headers = { headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }};

  const eventId = setupData.eventId;

  // pick a random seat
  const seat = `S${Math.floor(Math.random() * 100) + 1}`;

  // 1) Create hold (POST /holds)
  // OpenAPI expects { event_id, seat_nos: [...] }
  let holdRes = http.post(`${BASE_URL}/holds`, JSON.stringify({
    event_id: eventId,
    seat_nos: [seat],
  }), headers);

  // record
  if (holdRes.status === 201) {
    holdsCreated.add(1);
  } else if (holdRes.status === 409) {
    holdsConflict.add(1);
  } else {
    // capture auth or server errors
    if (holdRes.status === 401) http401.add(1);
    if (holdRes.status >= 500) http500.add(1);
    unexpectedErrors.add(1);
    console.error('HOLD FAILED', holdRes.status, holdRes.body);
  }

  // parse hold token if present
  let holdToken = null;
  if (holdRes.status === 201) {
    try {
      holdToken = JSON.parse(holdRes.body).hold_token;
    } catch (e) {
      console.error('HOLD: cannot parse hold response', holdRes.body);
    }
  }

  // 2) Try booking using hold token (if we have one)
  if (holdToken) {
    const idemKey = `k6-${__VU}-${Date.now()}-${Math.floor(Math.random()*1000)}`;
    const bookRes = http.post(`${BASE_URL}/bookings`, JSON.stringify({
      event_id: eventId,
      hold_token: holdToken,
    }), {
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': 'application/json',
        'Idempotency-Key': idemKey,
      },
    });

    if (bookRes.status === 201) {
      bookingsCreated.add(1);
      successRate.add(1);
    } else if (bookRes.status === 200) {
      bookingsIdem.add(1);
    } else if (bookRes.status === 409) {
      bookingsConflict.add(1);
      console.warn('BOOK CONFLICT', bookRes.status, bookRes.body);
    } else {
      if (bookRes.status === 401) http401.add(1);
      if (bookRes.status >= 500) http500.add(1);
      unexpectedErrors.add(1);
      console.error('BOOK FAILED', bookRes.status, bookRes.body);
    }

    // Optionally cancel (10% chance)
    if (Math.random() < 0.1 && bookRes.status === 201) {
      let bookingId = null;
      try {
        bookingId = JSON.parse(bookRes.body).id;
      } catch (e) {
        console.error('BOOK: cannot parse booking id', bookRes.body);
      }
      if (bookingId) {
        const cancelRes = http.del(`${BASE_URL}/bookings/${bookingId}`, null, { headers: { Authorization: `Bearer ${token}` }});
        if (cancelRes.status === 200) {
          cancels.add(1);
        } else {
          unexpectedErrors.add(1);
          console.error('CANCEL FAILED', cancelRes.status, cancelRes.body);
        }
      }
    }
  }

  // 3) Occasionally join waitlist (10%)
  if (Math.random() < 0.1) {
    const waitRes = http.post(`${BASE_URL}/events/${eventId}/waitlist`, JSON.stringify({
      requested_seats: 1,
    }), headers);

    if (waitRes.status === 202) {
      waitlists.add(1);
    } else if (waitRes.status === 409 || (waitRes.body && waitRes.body.includes('duplicate key'))) {
      waitlistsConflict.add(1);
      // duplicate waitlist attempts are fine; log for visibility
      console.warn('WAITLIST DUPLICATE', waitRes.status, waitRes.body);
    } else {
      if (waitRes.status === 401) http401.add(1);
      if (waitRes.status >= 500) http500.add(1);
      unexpectedErrors.add(1);
      console.error('WAITLIST FAILED', waitRes.status, waitRes.body);
    }
  }

  // checks: hold should be ok or conflict
  check(holdRes, {
    'hold ok/conflict': (r) => [201, 409].includes(r.status),
  });

  sleep(Math.random() * 1.5 + 0.2);
}

// ---------------- custom summary ----------------
export function handleSummary(data) {
  console.log('\n==== CUSTOM K6 SUMMARY ====');
  console.log('holds_created:', data.metrics.holds_created ? data.metrics.holds_created.values.count : 'n/a');
  console.log('holds_conflict:', data.metrics.holds_conflict ? data.metrics.holds_conflict.values.count : 'n/a');
  console.log('bookings_created:', data.metrics.bookings_created ? data.metrics.bookings_created.values.count : 'n/a');
  console.log('bookings_idem:', data.metrics.bookings_idempotent ? data.metrics.bookings_idempotent.values.count : 'n/a');
  console.log('bookings_conflict:', data.metrics.bookings_conflict ? data.metrics.bookings_conflict.values.count : 'n/a');
  console.log('cancels:', data.metrics.cancels ? data.metrics.cancels.values.count : 'n/a');
  console.log('waitlists:', data.metrics.waitlists ? data.metrics.waitlists.values.count : 'n/a');
  console.log('waitlists_conflict:', data.metrics.waitlists_conflict ? data.metrics.waitlists_conflict.values.count : 'n/a');
  console.log('unexpected_errors:', data.metrics.unexpected_errors ? data.metrics.unexpected_errors.values.count : 'n/a');
  console.log('http_401:', data.metrics.http_401 ? data.metrics.http_401.values.count : 'n/a');
  console.log('http_500:', data.metrics.http_500 ? data.metrics.http_500.values.count : 'n/a');
  console.log('============================\n');

  // Default JSON summary to stdout as well
  return {
    stdout: JSON.stringify(data),
  };
}
