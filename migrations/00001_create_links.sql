-- +goose Up
CREATE TABLE links (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code text COLLATE "C" NOT NULL,
    original_url text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT links_code_key UNIQUE (code),
    CONSTRAINT links_code_format CHECK (code ~ '^[A-Za-z0-9_-]{3,32}$'),
    CONSTRAINT links_original_url_length CHECK (octet_length(original_url) BETWEEN 1 AND 2048)
);

-- +goose Down
DROP TABLE links;
