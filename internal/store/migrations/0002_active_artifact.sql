-- Pin each app to an explicit artifact instead of inferring one at launch.
--
-- Until now the supervisor picked the most recently uploaded jar, which means
-- uploading a jar for review would silently change what the next restart runs.
-- Promotion is now a separate, deliberate action, and it is also what makes
-- rollback possible: the previous artifact stays in the repository and
-- promoting it back is one call.
ALTER TABLE apps ADD COLUMN active_artifact_id INTEGER REFERENCES artifacts (id);

CREATE INDEX idx_artifacts_app ON artifacts (app_id, uploaded_at DESC);
