# SwiftKV
A lightweight Redis clone built in **Go**, implementing core Redis features including key–value storage, lists, sorted sets, transactions, replication, pub/sub, persistence, and more.

This project is designed to **learn Redis internals** by building it from scratch — covering networking, the RESP protocol, concurrency, and data structures.

---

## 🚀 Features

### ✅ Core Commands

* `PING`, `ECHO`
* `SET`, `GET`, `TYPE`, `INCR`
* `CONFIG GET`, `KEYS`, `INFO`
* Expiry with `PX` option

### ✅ Lists

* `RPUSH`, `LPUSH`, `LPOP`, `LRANGE`, `LLEN`

### ✅ Transactions

* `MULTI`, `EXEC`, `DISCARD`

### ✅ RDB Persistence

* Supports reading/writing RDB file format (`dump.rdb`)
* `CONFIG GET dir`, `CONFIG GET dbfilename`

### ✅ Sorted Sets (ZSets)

* `ZADD`, `ZRANK`, `ZRANGE`, `ZCARD`, `ZREM`

### ✅ Pub/Sub

* `SUBSCRIBE`, `UNSUBSCRIBE`, `PUBLISH`

### ✅ Replication

* Implements leader–follower replication via `REPLCONF` and `PSYNC`

---

## 📦 Getting Started

```bash
# Clone the repo
git clone https://github.com/bodyhedia44/SwiftKV.git
cd SwiftKV

# Build
go build -o SwiftKV .

# Run
./SwiftKV --port 6379
```

Then connect with the official `redis-cli` or any Redis client library:

```bash
redis-cli -p 6379
127.0.0.1:6379> SET foo bar
OK
127.0.0.1:6379> GET foo
"bar"
```

---

## 🛠 Project Goals

* Understand Redis protocol (RESP) and networking internals.
* Explore in-memory storage, persistence, and replication.
* Implement real Redis commands from scratch.
* Learn concurrency, locking, and client handling in Go.

---

## 📚 Roadmap

* [x] Core key–value commands
* [x] Lists
* [x] Transactions
* [x] Persistence (RDB)
* [x] Sorted Sets
* [x] Pub/Sub
* [x] Replication
---

## ⚡ Tech Stack

* **Go** — for server + concurrency
* **RESP protocol** — for client communication
* **In-memory data structures** (maps, slices, sorted sets)

---
