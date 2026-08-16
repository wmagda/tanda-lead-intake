-- v2: make AI-parse failures retryable instead of permanently skipped.
-- When LM Studio is down, Process() used to record a permanent "skipped"
-- row; a transient outage silently ate the lead. Now AI failures get
-- status='ai_failed' + a backoff schedule, and the worker re-runs them.

alter table email_threads
  add column if not exists status       text,       -- null=processed/lead-attached | 'ai_failed' | 'skipped'
  add column if not exists retry_count  int not null default 0,
  add column if not exists next_retry_at timestamptz;

create index if not exists idx_email_threads_retryable
  on email_threads(status, next_retry_at)
  where status = 'ai_failed';
