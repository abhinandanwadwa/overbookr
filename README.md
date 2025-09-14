# 🎟️ Overbookr

A scalable backend system for **event seat-level bookings**.
Built with **Go (Gin framework)** and **PostgreSQL**, designed to handle **high concurrency, idempotent bookings, seat holds, and waitlists**.

---

## ✨ Features

* 👤 **User Management** – Register, login (JWT-based authentication), roles (`user`, `admin`)
* 🎫 **Event Management** – Create, list, and view events with seat capacity
* 💺 **Seat-Level Reservations** – Bulk insert seats, query seat maps
* ⏳ **Seat Holds** – Temporarily reserve seats with a hold token (5 minutes)
* 🛡 **Idempotent Bookings** – Prevents duplicate bookings with idempotency keys
* 📋 **Waitlist** – Users can queue when an event is full, auto-promoted when seats free
* ❌ **Cancellations** – Cancel bookings safely and trigger waitlist promotions
* 📊 **Analytics** – Bookings per day, cancellations, utilization, and top events
* ⚡ **Background Workers** – Expire holds, promote waitlists, reconcile mismatches

---

## 🛠 Tech Stack

* **Language:** Go (Gin framework)
* **Database:** PostgreSQL + SQLC for queries
* **Auth:** JWT (role-based)
* **Deployment:** [Railway](http://overbookr-production.up.railway.app/)
* **Documentation:** [Swagger](http://overbookr-production.up.railway.app/docs)
* **Load Testing:** k6

---

## 📂 Project Structure

```
.
├── cmd/               # Main entrypoint
├── internal/          
│   ├── api/           # Full API logic
│   ├── db/            # SQLC generated queries
│   ├── queries/       # Plain SQL Queries
│   └── workers/       # Background workers
├── migrations/        # SQL migrations
└── README.md
```

---

## ⚙️ Setup & Run

### 1. Clone Repo

```bash
git clone https://github.com/abhinandanwadwa/.git
cd <repo-name>
```

### 2. Environment Variables

Create a `.env` file with the following:

```env
# Server
POSTGRESQL_URI="postgresql://user:password@localhost:5432/overbookr?sslmode=disable"
PORT="8080"

JWT_SECRET="your_jwt_secret_key_here"

GMAIL_USER="your_email_address"
GMAIL_PASS="your_email_password(app_passwords are recommended)"
```

### 3. Run Migrations

```bash
migrate -path migrations -database "$POSTGRESQL_URI" up
```

*(or use `goose`/`atlas` depending on your migration tool)*

### 4. Start API

```bash
go run cmd/main.go
```

### 5. Run Tests

```bash
k6 run internal/api/tests/k6_full_load.js
```

---

## 🏗 Architecture

![Architecture Diagram](./internal/api/server/docs/architecture.png)

* Clients → API Layer (Gin) → PostgreSQL
* Background Workers:

  * Promote waitlists when seats free
  * Expire holds every 30s
  * Reconcile mismatches hourly

---

## 📊 ER Diagram

![ER Diagram](./internal/api/server/docs/er-diagram.png)

* **Users** ↔ **Bookings** ↔ **Seats** ↔ **Events**
* **Seat Holds** reserve seats before booking
* **Waitlist** queues users for full events

---

## 🔑 Design Decisions & Tradeoffs

* **Atomic Booked Count Guard**
  Prevent overselling by only incrementing `booked_count` if it stays under capacity.

* **Seat Holds First, Book Later**
  Users can’t directly book seats. They first create a **hold**, then confirm with a hold token. This avoids race conditions.

* **Idempotency Keys**
  Guarantees duplicate booking requests don’t create multiple bookings.

* **Tradeoff: Waitlist Ordering**
  Current implementation uses `MAX(position)+1` which works, but under heavy concurrency, an **event-level counter** or **per-event sequence** would be stronger.

* **Background Reconciliation**
  Periodically fixes mismatches. In production, we’d prefer logging + alerting instead of silent auto-fix.

---

## 📈 Load Testing Results

📊 **k6 Test with 50 VUs for \~85s**

* **Requests handled:** \~5,300 (\~62 req/s)
* **Avg response time:** 1.2 ms
* **p95 latency:** 1.46 ms (99% of requests <2ms)
* **Max latency:** \~61 ms
* **Bookings created:** 96 (100% consistency with holds)
* **Conflicts handled:** \~2272 (all returned 409, no double-bookings)
* **Waitlists joined:** 45 successful, 214 duplicates rejected correctly
* **Cancellations:** 12 successful, all freed seats cleanly

✅ **Highlights:**

* Concurrency-safe: no double bookings under race conditions
* Data integrity preserved under stress
* Blazing fast: 99% of requests <2ms
* Resilient workers: holds expired, waitlists promoted, mismatches reconciled

---

## 🚀 Future Improvements

* Redis-backed seat holds for even faster operations at scale
* Message queues for large-scale waitlist promotions
* Prometheus/Grafana for observability
* Sharding or partitioning events across DBs for extreme scale
