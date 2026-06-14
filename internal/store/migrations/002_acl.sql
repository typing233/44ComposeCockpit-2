-- +goose Up
CREATE TABLE project_acl (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    role       TEXT NOT NULL CHECK (role IN ('operator', 'viewer')),
    granted_by UUID REFERENCES users(id),
    granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, project_id)
);

CREATE INDEX idx_acl_project ON project_acl(project_id);
CREATE INDEX idx_acl_user    ON project_acl(user_id);

-- +goose Down
DROP TABLE IF EXISTS project_acl;
