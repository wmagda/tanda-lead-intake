# tanda — Salsa Collective Lead Intake

AI-powered inbox that never loses a lead.

- **Repo:** https://github.com/wmagda/tanda-lead-intake
- **Live site:** https://salsa-collective.com/
- **Admin route (future):** `/admin/inbox`

---

## Tech stack

| Layer         | Choice                        |
|---------------|-------------------------------|
| Backend       | Go (Gin) — local + Cloud Run |
| Database      | Supabase Postgres             |
| AI inference  | LM Studio / Nous (local)     |
| Email         | Gmail API (polling → webhook) |
| Admin UI      | Lovable (extends existing)    |

---

## Quick start (local)

```bash
git clone https://github.com/wmagda/tanda-lead-intake.git
cd tanda-lead-intake

cp .env.example .env
# edit .env — set DATABASE_URL + LMSTUDIO_BASE_URL

docker compose up -d      # local Postgres for testing
make migrate-up           # apply supabase/migrations/v1__init.sql
go run ./cmd/server
```

---

## Project structure

```
tanda-lead-intake/
├── cmd/server/              # binary entry-point
├── internal/
│   ├── db/                  # pgx pool wrapper
│   ├── handlers/            # Gin HTTP handlers
│   ├── models/              # Go structs matching Supabase tables
│   ├── email/               # AI inference client (LM Studio)
│   ├── gmail/               # Gmail polling + send reply
│   └── ai/                  # future: Vertex AI fallback
├── supabase/
│   └── migrations/          # SQL schema (v1 — init)
├── ui/admin/                # Lovable admin UI overlay notes
├── docker-compose.yml       # local Postgres
└── docs/                    # design documents
```

---

## Endpoints

| Method | Route                         | Purpose                        |
|--------|-------------------------------|--------------------------------|
| GET    | `/healthz`                    | liveness check                 |
| GET    | `/api/leads`                  | admin queue (status filters)   |
| GET    | `/api/leads/:id`              | lead detail + thread + draft   |
| POST   | `/api/leads/:id/approve`      | approve draft → send reply     |
| POST   | `/api/leads/:id/task`         | create follow-up task          |
| POST   | `/api/email/process`          | ingest a new email             |

---

## Lovable Admin UI

Add route `/admin/inbox` to the existing Salsa Collective admin panel.
See `ui/admin/README.md` for component specs and API integration.

---

## .env requirements

| Variable           | Notes                                      |
|--------------------|--------------------------------------------|
| `DATABASE_URL`     | Supabase pooler or local Postgres          |
| `LMSTUDIO_BASE_URL`| e.g. `http://localhost:1234/v1`            |
| `GMAIL_CREDENTIALS`| Path to service-account JSON               |
| `GMAIL_USER_EMAIL` | The studio inbox address                    |
| `PORT`             | Default: 8080                              |

---

## Status

🚧 MVP scaffolding — backend endpoints, DB schema, and Docker Compose are in place.  
AI parsing and Gmail polling are stubbed; implementation is next.
