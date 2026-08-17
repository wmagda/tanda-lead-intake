# tanda — Salsa Collective Lead Intake

AI-powered inbox that never loses a lead.

- **Repo:** https://github.com/wmagda/tanda-lead-intake
- **Live site:** https://salsa-collective.com/
- **Admin UI (Lovable):** `/admin/inbox` on the existing Salsa Collective admin panel

---

## What this project does

1. **Watches the studio Gmail inbox** (polling; webhook later).
2. **Parses each new message** with a local LLM (LM Studio) into structured lead fields.
3. **Writes to Postgres** (Supabase in prod, Docker locally): lead, email thread, and a **pending draft reply**.
4. **Sends approved replies and task notifications** via Gmail when Lovable marks them ready in the DB.
5. **Does not expose an HTTP API** — Lovable talks to Supabase only.

The Go worker can run on your Mac or a small VM without exposing ports to the internet.

---

## Architecture

```mermaid
flowchart LR
  Gmail[Gmail inbox] --> Worker[Go worker]
  Worker --> AI[LM Studio]
  AI --> Worker
  Worker --> DB[(Postgres / Supabase)]
  Lovable[Lovable admin] --> DB
  Worker --> Gmail
```

| Component | Responsibility |
|-----------|----------------|
| **Go worker** (`cmd/worker`) | Gmail poll → ingest → DB; send approved drafts + task emails |
| **Postgres** | `leads`, `email_threads`, `draft_responses`, `tasks` |
| **Lovable** | Inbox UI, edit drafts, approve, assign tasks, update status |
| **LM Studio** | OpenAI-compatible `/v1/chat/completions` on your LAN |

Lovable never calls the Go worker. Use the same `DATABASE_URL` (Supabase) in prod and local Docker for dev.

---

## Data written on ingest

For each new Gmail message, the worker (or `cmd/process-email` in tests):

| Table | Content |
|-------|---------|
| `leads` | Customer, intent (`request_type`), style, level, confidence, `status=new` |
| `email_threads` | Raw message (deduped by `gmail_message_id`; `lead_id` NULL for skipped non-leads) |
| `draft_responses` | **Proposed reply** — `draft_text`, `approval_status=pending` |

**Outbound mail:** Lovable sets `draft_responses.approval_status=approved`. The worker polls and sends via Gmail, then sets `sent_at`. Task assignee emails work the same way via `tasks.notified_at`.

---

## Tech stack

| Layer | Choice |
|-------|--------|
| Email worker | Go 1.23 |
| Database | Supabase Postgres (local: `postgres:15-alpine` in Docker) |
| AI | LM Studio / OpenAI-compatible API |
| Gmail | OAuth2 (`credentials.json` + `token.json`) |
| Admin UI | Lovable + Supabase client |

---

## Quick start (local)

### 1. Clone and configure

```bash
git clone https://github.com/wmagda/tanda-lead-intake.git
cd tanda-lead-intake
cp .env.example .env
```

Edit `.env`: `DATABASE_URL`, `OPENAI_*` (LM Studio host/model), optional `OPENAI_TIMEOUT=10m` for slow models.

### 2. Database

```bash
docker compose up -d
```

On first boot, `supabase/migrations/v1__init.sql` runs automatically. Verify:

```bash
docker exec -it tanda-db psql -U tanda -d tanda -c '\dt'
```

Local URL (default):

```env
DATABASE_URL=postgresql://tanda:tanda_dev_pass@localhost:54322/tanda?sslmode=disable
```

### 3. Gmail OAuth (once per inbox)

```bash
go run ./cmd/generate_token
```

Saves `token.json`. Add test users in Google Cloud OAuth consent screen if needed.

### 4. Test ingest (one fake email)

Start LM Studio with your model loaded. Optional: enable **structured JSON output** in LM Studio if the model supports it.

```bash
make ingest-test
```

Expect log output like:

```text
status=created lead_id=... thread_id=... draft_id=... intent=private_lesson
```

Inspect the proposed reply:

```bash
docker exec -it tanda-db psql -U tanda -d tanda -c \
  "SELECT l.customer_email, d.approval_status, left(d.draft_text, 200)
   FROM draft_responses d
   JOIN leads l ON l.id = d.lead_id
   ORDER BY d.created_at DESC LIMIT 3;"
```

### 5. Run the worker (background)

```bash
make run
```

Polls Gmail on `GMAIL_POLL_INTERVAL` (default `2m`), ingests new messages, and runs the send loop on `SEND_POLL_INTERVAL` (default `30s`).

---

## Project structure

```
tanda-lead-intake/
├── cmd/
│   ├── worker/           # long-running Gmail + ingest + send worker
│   ├── process-email/    # one-shot ingest CLI for local testing
│   └── generate_token/   # Gmail OAuth setup
├── internal/
│   ├── db/               # Postgres pool
│   ├── ingest/           # dedupe, AI parse, transactional DB writes
│   ├── ai/               # LM Studio client + prompts
│   ├── gmail/            # polling, approved-draft send, task notifications
│   ├── parseutil/        # sender/contact/phone helpers
│   └── models/           # Lead DTO from AI parse (partial table mirror)
├── supabase/migrations/  # schema (v1)
├── ui/admin/             # Lovable UI design notes
├── docker-compose.yml    # local Postgres on :54322
└── Makefile
```

---

## Commands

| Command | Purpose |
|---------|---------|
| `make docker-up` | Start local Postgres |
| `make run` | Run Gmail worker |
| `make ingest-test` | Simulate one inbound email through AI + DB |
| `go run ./cmd/generate_token` | Refresh Gmail OAuth token |
| `go test ./...` | Unit tests |

---

## System prompt

The AI's intake instructions (studio details, pricing, phone, contact addresses)
live in a **gitignored** file, `prompts/intake-system.prompt`, because this repo is
public and that file contains private business information. It is loaded at startup
via `AI_SYSTEM_PROMPT_FILE` (default `prompts/intake-system.prompt`).

- A business-neutral template ships as `prompts/intake-system.prompt.example`.
- Copy it to `prompts/intake-system.prompt` and fill in your real details.
- If the file is missing, the worker uses a generic prompt so it still runs.
- Never commit `prompts/intake-system.prompt` — it is gitignored on purpose.

## Environment variables

| Variable | Required | Notes |
|----------|----------|-------|
| `DATABASE_URL` | yes | Supabase pooler or local Docker URL |
| `OPENAI_BASE_URL` | for AI | LM Studio base; `/v1` appended if missing |
| `OPENAI_MODEL` | for AI | Exact model id as shown in LM Studio |
| `OPENAI_API_KEY` | for AI | LM Studio API key |
| `OPENAI_TIMEOUT` | no | Default `10m` — increase for slow local models |
| `OPENAI_RETRY_MAX` | no | LLM retries on transient errors (default `3`) |
| `OPENAI_RETRY_BASE` | no | Backoff base (default `2s`) |
| `AI_SYSTEM_PROMPT_FILE` | no | Path to the intake system prompt (default `prompts/intake-system.prompt`); if missing a generic prompt is used |
| `GMAIL_CREDENTIALS` | for Gmail | Path to OAuth client JSON |
| `GMAIL_TOKEN` | for Gmail | Path to `token.json` |
| `GMAIL_USER_EMAIL` | for Gmail | Inbox to monitor |
| `GMAIL_FORM_FROM` | no | Comma-separated form relay addresses (e.g. Resend) |
| `GMAIL_INITIAL_LOOKBACK` | no | First poll window on startup (default `24h`, try `7d` for backfill) |
| `GMAIL_POLL_INTERVAL` | no | Inbox poll interval (default `2m`) |
| `SEND_POLL_INTERVAL` | no | Approved drafts + task notify poll (default `30s`) |
| `LOG_LEVEL` | no | Logging verbosity (default `info`) |
| `ENV` | no | Deployment environment label (default `local`) |

---

## Lovable admin UI

See [`ui/admin/README.md`](ui/admin/README.md) for screen specs.

Lovable should use the **Supabase JavaScript client** to:

- List/filter `leads` and join `draft_responses` / `email_threads`
- Show and edit `draft_responses.draft_text`
- On **Approve**: set `approval_status=approved` (worker sends email and sets `sent_at`)
- Manage `tasks` (`assignee_email`, `assigned_to`) and `leads.status`

---

## Safety

- Ingest always creates drafts with `approval_status = pending`.
- **Customer email is only sent** after a human approves in Lovable (`approval_status=approved`).
- The worker sends from the studio Gmail account; it does not auto-send on ingest.
- Re-run `go run ./cmd/generate_token` if OAuth `token.json` expires.

---

## Production notes

- Run the worker on a always-on host (VM, Cloud Run job, etc.) with env pointing at **Supabase** and LM Studio reachable on your network (or swap to a hosted model later).
- Point Lovable at the **same Supabase project** as the worker.
- Do not commit `credentials.json`, `token.json`, or `.env`.

---

## Status

| Area | State |
|------|--------|
| Schema + local Docker | Done |
| Ingest + AI parse + pending drafts | Done |
| `make ingest-test` | Working |
| Gmail polling → ingest | Done |
| LLM lead filtering + contact extraction | Done |
| Retry on transient AI failures | Done |
| Approved draft + task email send | Done |
| Lovable `/admin/inbox` | Ready (schema + queries documented) |
| Operational / vendor inbox (non-leads) | Not started |
| Venmo / payments | Deferred |
