-- +goose Up
CREATE TABLE visits (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    link_id bigint NOT NULL REFERENCES links(id),
    visited_at timestamptz NOT NULL,
    user_agent text NOT NULL DEFAULT '',
    CONSTRAINT visits_user_agent_length CHECK (octet_length(user_agent) <= 1024)
);

CREATE INDEX visits_link_time_id_idx ON visits (link_id, visited_at DESC, id DESC);

-- +goose Down
DROP TABLE visits;
