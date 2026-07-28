-- Create trigger function to update updated_at timestamps automatically
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ language 'plpgsql';

-- 1. Installations Table
CREATE TABLE installations (
    id BIGSERIAL PRIMARY KEY,
    github_installation_id BIGINT UNIQUE NOT NULL,
    account_login TEXT NOT NULL,
    account_type TEXT NOT NULL,
    suspended_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TRIGGER update_installations_updated_at
    BEFORE UPDATE ON installations
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- 2. GitHub Accounts Table (Reviewer Pool)
CREATE TABLE github_accounts (
    id BIGSERIAL PRIMARY KEY,
    github_login TEXT UNIQUE NOT NULL,
    access_token_enc BYTEA NULL,
    token_expires_at TIMESTAMPTZ NULL,
    status TEXT NOT NULL DEFAULT 'active',
    last_used_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_status CHECK (status IN ('active', 'invalid', 'disabled'))
);

CREATE TRIGGER update_github_accounts_updated_at
    BEFORE UPDATE ON github_accounts
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- 3. Review Jobs Table (Queue and State)
CREATE TABLE review_jobs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    installation_id BIGINT NOT NULL REFERENCES installations(id) ON DELETE CASCADE,
    repo_full_name TEXT NOT NULL,
    pr_number INT NOT NULL,
    head_sha TEXT NOT NULL,
    base_sha TEXT NULL,
    publisher_account_id BIGINT NULL REFERENCES github_accounts(id) ON DELETE SET NULL,
    status TEXT NOT NULL,
    lock_generation BIGINT NOT NULL DEFAULT 0,
    locked_until TIMESTAMPTZ NULL,
    locked_by TEXT NULL,
    attempt INT NOT NULL DEFAULT 0,
    last_error TEXT NULL,
    context_blob JSONB NULL,
    result_blob JSONB NULL,
    prompt_version TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (installation_id, repo_full_name, pr_number, head_sha),
    CONSTRAINT chk_job_status CHECK (status IN (
        'pending', 'running', 'retry_wait', 'completed', 'failed', 'cancelled', 'needs_reconcile'
    ))
);

CREATE TRIGGER update_review_jobs_updated_at
    BEFORE UPDATE ON review_jobs
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

CREATE INDEX idx_review_jobs_status_locked_until ON review_jobs (status, locked_until);
CREATE INDEX idx_review_jobs_repo_pr ON review_jobs (repo_full_name, pr_number);

-- 4. Review Publications Table (Publishing Fencing)
CREATE TABLE review_publications (
    id BIGSERIAL PRIMARY KEY,
    job_id UUID NOT NULL UNIQUE REFERENCES review_jobs(id) ON DELETE CASCADE,
    publisher_account_id BIGINT NULL REFERENCES github_accounts(id) ON DELETE SET NULL,
    publisher_kind TEXT NOT NULL DEFAULT 'user',
    state TEXT NOT NULL,
    github_review_id BIGINT NULL,
    published_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT chk_pub_state CHECK (state IN ('pending_github', 'published', 'failed')),
    CONSTRAINT chk_pub_kind CHECK (publisher_kind IN ('user', 'app_bot'))
);

CREATE TRIGGER update_review_publications_updated_at
    BEFORE UPDATE ON review_publications
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- 5. Project Rules Table (Thin Memory Stub)
CREATE TABLE project_rules (
    id BIGSERIAL PRIMARY KEY,
    installation_id BIGINT NOT NULL REFERENCES installations(id) ON DELETE CASCADE,
    repo_full_name TEXT NOT NULL,
    rule_key TEXT NOT NULL,
    rule_body TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (installation_id, repo_full_name, rule_key)
);

CREATE TRIGGER update_project_rules_updated_at
    BEFORE UPDATE ON project_rules
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();
