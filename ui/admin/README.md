# Lovable Admin UI — `/admin/inbox` Design Notes

This folder holds the front-end design reference for the tanda admin queue
that lives inside the existing Salsa Collective Lovable admin panel.

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

Actions (primary buttons):
- `Approve & Send` → `POST /api/leads/:id/approve`
- `Save Draft` → persist edited draft_text
- `Add Task` → `POST /api/leads/:id/task`
- `Mark Closed` → `PUT /api/leads/:id` with `status=closed`

### Dashboard Widgets

| Widget              | Source               |
|---------------------|----------------------|
| New leads (last 24h)| `leads.created_at`   |
| Awaiting approval   | `leads.status` = …   |
| Overdue follow-ups  | `tasks.due_date`     |
| Conversion rate     | `leads.status=booked`/`created` |

Estimated MVP effort: 1 screen (queue) + 1 detail page + 3 widgets = ~2 weeks
for a single Lovable dev familiar with the existing codebase.
