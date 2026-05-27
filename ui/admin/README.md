# Lovable Admin UI — `/admin/inbox` Design Notes

This folder holds the front-end design reference for the tanda admin queue
that lives inside the existing Salsa Collective Lovable admin panel.

**Integration model:** The Go worker in this repo only **writes** to Postgres
(leads, threads, pending drafts). Lovable reads and updates the same Supabase
tables directly — it does **not** call the Go worker over HTTP.

## Component map

### InboxQueue (route: `/admin/inbox`)

Displays the lead pipeline as Kanban-style lanes or a sortable table.

Columns / lanes:
- New
- Awaiting Approval
- Waiting Customer Reply
- Scheduled
- Completed

Card fields displayed:
- Customer name, email
- Request type badge (`private_lesson`, `group_class`, `pricing`, …)
- Dance style + level
- Student count
- AI confidence (0–100%)
- `created_at`

Filters (sidebar):
- Status (multi-select)
- Request type
- Dance style
- Date range

### LeadDetail (route: `/admin/inbox/:id`)

Sections:
1. **Header** — customer info, status badge, priority, created date
2. **Email thread** — chronological message list (from `email_threads`)
3. **AI parse result** — structured fields (intent, dance style, level, …)
4. **Draft editor** — rich text editor bound to `draft_responses.draft_text`
5. **Tasks** — follow-up task list from `tasks`
6. **Activity log** — notes + status transitions

Actions (primary buttons) — via **Supabase** / Lovable backend (not the Go worker):

- `Approve & Send` → update `draft_responses` (`approval_status=approved`), send Gmail from Lovable, set `sent_at`
- `Save Draft` → update `draft_responses.draft_text`
- `Add Task` → insert into `tasks`
- `Mark Closed` → update `leads.status`

The Go worker only **ingests** inbound mail and creates `pending` drafts. It never sends email.

### Dashboard Widgets

| Widget              | Source               |
|---------------------|----------------------|
| New leads (last 24h)| `leads.created_at`   |
| Awaiting approval   | `leads.status` = …   |
| Overdue follow-ups  | `tasks.due_date`     |
| Conversion rate     | `leads.status=booked`/`created` |

Estimated MVP effort: 1 screen (queue) + 1 detail page + 3 widgets = ~2 weeks
for a single Lovable dev familiar with the existing codebase.

---

## Supabase connection

The Go worker and Lovable both connect to the **same Supabase project**.

- **Lovable** uses the Supabase JS client with the project URL + anon key (configured in your Lovable project settings).
- **Go worker** uses `DATABASE_URL` with the Supabase pooler connection string (Session mode) or direct URI.

Both see the same tables: `leads`, `email_threads`, `draft_responses`, `tasks`.

---

## Query reference (for Lovable)

### Inbox list (all leads, newest first)

```sql
select l.*,
  (select count(*) from email_threads et where et.lead_id = l.id) as message_count,
  (select draft_text from draft_responses dr
   where dr.lead_id = l.id order by dr.created_at desc limit 1) as latest_draft
from leads l
order by l.created_at desc;
```

Or with Supabase JS:

```ts
const { data } = await supabase
  .from('leads')
  .select('*, email_threads(count), draft_responses(draft_text)')
  .order('created_at', { ascending: false });
```

### Lead detail

```ts
// lead + threads + drafts + tasks in one call
const { data: lead } = await supabase
  .from('leads')
  .select(`
    *,
    email_threads ( id, sender_email, subject, body, received_at ),
    draft_responses ( id, draft_text, approval_status, created_at ),
    tasks ( id, task_type, status, assigned_to, due_date, notes, created_at )
  `)
  .eq('id', leadId)
  .single();
```

### Update lead status

```ts
await supabase.from('leads').update({ status: 'closed' }).eq('id', leadId);
```

### Approve draft

```ts
await supabase
  .from('draft_responses')
  .update({ approval_status: 'approved', approved_by: user.email })
  .eq('id', draftId);
```

### Edit draft

```ts
await supabase
  .from('draft_responses')
  .update({ draft_text: newText })
  .eq('id', draftId);
```

### Create task

```ts
await supabase.from('tasks').insert({
  lead_id: leadId,
  task_type: 'follow_up',
  status: 'open',
  notes: 'Call back about pricing',
  due_date: '2026-06-01T10:00:00Z',
});
```

### Dashboard widgets

```ts
// New leads in last 24h
const { count } = await supabase
  .from('leads')
  .select('*', { count: 'exact', head: true })
  .gte('created_at', new Date(Date.now() - 86400000).toISOString());

// Leads awaiting approval (have pending drafts)
const { count: pending } = await supabase
  .from('draft_responses')
  .select('*', { count: 'exact', head: true })
  .eq('approval_status', 'pending');
```

---

## Schema columns (leads)

| Column | Type | Notes |
|--------|------|-------|
| id | uuid | PK, auto-generated |
| gmail_thread_id | text | unique, links to Gmail |
| customer_name | text | extracted by AI |
| customer_email | text | extracted by AI or envelope |
| customer_phone | text | extracted by AI or body parse |
| request_type | text | private_lesson, group_class, pricing, etc. |
| dance_style | text | salsa, bachata, kizomba, etc. |
| level | text | beginner, intermediate, advanced |
| student_count | integer | number of students |
| requested_time | text | free-text scheduling preference |
| status | text | new, awaiting_approval, scheduled, closed |
| priority | text | normal, high |
| ai_confidence | numeric | 0-1 |
| notes | text | auto-appended ingest notes |
| created_at | timestamptz | auto |
| updated_at | timestamptz | auto (trigger) |
