-- v1__init.sql
-- Tanda Lead Intake — initial schema
-- Run in Supabase SQL Editor or via migration tooling

-- ── leads ──────────────────────────────────────────────────────
create table if not exists leads (
  id              uuid primary key default gen_random_uuid(),
  gmail_thread_id text,
  customer_name   text,
  customer_email  text,
  customer_phone  text,
  request_type    text,          -- private_lesson | group_class | pricing | teacher_request | general_question
  dance_style     text,          -- salsa | bachata | kizomba | …
  level           text,          -- beginner | intermediate | advanced
  student_count   integer,
  requested_time  text,
  status          text not null default 'new',
  priority        text default 'normal',
  notes           text,
  ai_confidence   numeric,       -- 0-1 from LLM parse
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now()
);

create unique index if not exists idx_leads_gmail_thread_id on leads(gmail_thread_id);

create index if not exists idx_leads_status       on leads(status);
create index if not exists idx_leads_request_type on leads(request_type);
create index if not exists idx_leads_created_at    on leads(created_at desc);

-- ── email_threads ──────────────────────────────────────────────
create table if not exists email_threads (
  id               uuid primary key default gen_random_uuid(),
  lead_id          uuid references leads(id) on delete cascade,
  gmail_message_id text unique,
  gmail_thread_id  text,
  sender_email     text,
  subject          text,
  body             text,
  received_at      timestamptz not null default now()
);

create index if not exists idx_email_threads_lead_id       on email_threads(lead_id);
create index if not exists idx_email_threads_gmail_thread_id on email_threads(gmail_thread_id);

-- ── draft_responses ───────────────────────────────────────────
create table if not exists draft_responses (
  id              uuid primary key default gen_random_uuid(),
  lead_id         uuid references leads(id) on delete cascade,
  draft_text      text not null,
  approval_status text not null default 'pending',  -- pending | approved | rejected
  approved_by     text,
  sent_at         timestamptz,
  created_at      timestamptz not null default now()
);

create index if not exists idx_draft_responses_lead_id     on draft_responses(lead_id);
create index if not exists idx_draft_responses_approval_on  on draft_responses(approval_status, created_at);

-- ── tasks ─────────────────────────────────────────────────────
create table if not exists tasks (
  id          uuid primary key default gen_random_uuid(),
  lead_id     uuid references leads(id) on delete cascade,
  task_type   text not null,         -- follow_up | teacher_assign | reminder
  status      text not null default 'open',
  assigned_to text,
  due_date    timestamptz,
  notes       text,
  created_at  timestamptz not null default now()
);

create index if not exists idx_tasks_lead_id   on tasks(lead_id);
create index if not exists idx_tasks_status    on tasks(status);
create index if not exists idx_tasks_due_date  on tasks(due_date);

-- ── Row Level Security (RLS) ──────────────────────────────────
-- Permissive during MVP — tighten before production
alter table leads          enable row level security;
alter table email_threads  enable row level security;
alter table draft_responses enable row level security;
alter table tasks          enable row level security;

create policy "allow-all-authenticated" on leads          for all using (true);
create policy "allow-all-authenticated" on email_threads  for all using (true);
create policy "allow-all-authenticated" on draft_responses for all using (true);
create policy "allow-all-authenticated" on tasks          for all using (true);

-- ── Trigger: keep updated_at fresh ───────────────────────────
create or replace function touch_updated_at()
returns trigger as $$
begin
  new.updated_at = now();
  return new;
end;
$$ language plpgsql;

drop trigger if exists leads_updated_at on leads;
create trigger leads_updated_at before update on leads
  for each row execute function touch_updated_at();
