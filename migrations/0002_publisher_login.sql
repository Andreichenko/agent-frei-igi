-- Alter review_jobs to support storing publisher login string (intent) before OAuth connection
ALTER TABLE review_jobs ADD COLUMN publisher_login TEXT NULL;
