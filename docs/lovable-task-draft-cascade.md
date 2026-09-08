# Lovable fixes: cascade on close + dismiss draft

Hand this to the Lovable agent working on the Salsa Collective admin app.
All tables are in the same Supabase project the app already uses
(`leads`, `draft_responses`, `tasks`). No Go worker involvement needed for
these two fixes — they are pure app/DB behavior.

---

## 1. Dismiss a pending draft (new action, fixes un-clearable drafts)

Today the only actions on a draft are Approve & Send and Save Draft. There is
no way to discard a pending draft, so stale ones (e.g. a draft generated for a
"thank you, see you there!" that needed no reply) sit forever.

Add a third action on the draft editor:

```
Dismiss / Reject draft
```

Behavior:

```ts
await supabase
  .from('draft_responses')
  .update({ approval_status: 'rejected' })
  .eq('id', draftId);
```

This is non-destructive (keeps history, same status value the worker already
uses for superseded drafts). The UI should stop showing rejected drafts as
actionable.

---

## 2. Close cascades to drafts + tasks (fixes stale overdue tasks)

Today `Mark Closed` only does `leads.status = 'closed'`. It does NOT reject the
lead's pending drafts and does NOT close its open tasks. As a result a closed
conversation can still carry a pending draft and an overdue task that nobody
can find.

The app should do this atomically when closing a lead. Option A is recommended
(one call, done in the UI's Mark Closed handler). Option B is a DB trigger if
you prefer the cascade to happen regardless of which path closes the lead.

### Option A — in the app (recommended)

Replace the single `leads.update` on Mark Closed with a batch of updates inside
one transaction. With Supabase JS, use `rpc` against a Postgres function, or
sequential calls. The reliable way is a small Postgres function exposed as RPC:

```sql
create or replace function close_lead(p_lead_id uuid)
returns void
language plpgsql
security definer
as $$
begin
  update leads set status = 'closed', updated_at = now()
  where id = p_lead_id;

  update draft_responses set approval_status = 'rejected'
  where lead_id = p_lead_id and approval_status = 'pending';

  update tasks set status = 'done'
  where lead_id = p_lead_id and status = 'open';
end;
$$;
```

In the UI, the Mark Closed button calls:

```ts
const { error } = await supabase.rpc('close_lead', { p_lead_id: leadId });
```

Then reload the lead detail. That one call rejects pending drafts and closes
open tasks at the same time the lead closes.

### Option B — DB trigger (cascade on any `status='closed'` update)

If you'd rather not change the app code, this trigger does the cascade
automatically whenever a lead is set to closed from any code path:

```sql
create or replace function cascade_close_lead()
returns trigger
language plpgsql
as $$
begin
  if NEW.status = 'closed' and OLD.status is distinct from 'closed' then
    update draft_responses set approval_status = 'rejected'
    where lead_id = NEW.id and approval_status = 'pending';
    update tasks set status = 'done'
    where lead_id = NEW.id and status = 'open';
  end if;
  return NEW;
end;
$$;

drop trigger if exists trg_cascade_close_lead on leads;
create trigger trg_cascade_close_lead
  after update of status on leads
  for each row execute function cascade_close_lead();
```

---

## Verification checklist

After implementing either fix:

- Open a lead that has a pending draft → Dismiss button appears and moves it to
  rejected without deleting it.
- Mark a lead with an open task and a pending draft as Closed → the task
  becomes `done` and the draft becomes `rejected` in Supabase.
- The "Overdue follow-ups" dashboard widget should show 0 (it currently counts
  a stale open task tied to a `waiting_customer` lead that has since been
  closed).
