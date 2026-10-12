CREATE TABLE IF NOT EXISTS sandbox_jobs(
 id text PRIMARY KEY, input_hash text NOT NULL, container_name text NOT NULL,
 status text NOT NULL, result jsonb, created_at timestamptz NOT NULL DEFAULT now());
ALTER TABLE sandbox_jobs ADD COLUMN IF NOT EXISTS cleaned_at timestamptz;
CREATE INDEX IF NOT EXISTS sandbox_jobs_cleanup ON sandbox_jobs(created_at) WHERE cleaned_at IS NULL;
