-- Sprint 1: pipeline de texto checado.

CREATE TABLE sources (
    id       BIGSERIAL PRIMARY KEY,
    name     TEXT NOT NULL UNIQUE,
    kind     TEXT NOT NULL CHECK (kind IN ('rss', 'bcb', 'weather')),
    url      TEXT NOT NULL,
    license  TEXT NOT NULL DEFAULT 'none' -- 'cc-by' permite guardar o corpo
);

CREATE TABLE articles (
    id           BIGSERIAL PRIMARY KEY,
    source_id    BIGINT NOT NULL REFERENCES sources(id),
    url          TEXT NOT NULL UNIQUE,
    title        TEXT NOT NULL,
    title_hash   TEXT NOT NULL UNIQUE, -- sha256 do título normalizado (dedupe)
    summary      TEXT NOT NULL DEFAULT '',
    body         TEXT NULL,
    published_at TIMESTAMPTZ NULL,
    fetched_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX articles_published_idx ON articles (published_at DESC);

CREATE TABLE facts (
    id          BIGSERIAL PRIMARY KEY,
    article_id  BIGINT NULL REFERENCES articles(id),
    kind        TEXT NOT NULL CHECK (kind IN ('headline', 'market', 'weather')),
    claim       TEXT NOT NULL,
    entities    JSONB NOT NULL DEFAULT '[]',
    value       NUMERIC NULL,
    unit        TEXT NOT NULL DEFAULT '',
    as_of       TIMESTAMPTZ NOT NULL,
    source_name TEXT NOT NULL,
    source_url  TEXT NOT NULL,
    series      TEXT NULL, -- mercado/clima: 'bcb:432', 'weather:Recife'; NULL em manchetes
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Validade explícita: a reingestão do mesmo dado renova o prazo.
    expires_at  TIMESTAMPTZ NOT NULL,
    fingerprint TEXT NOT NULL UNIQUE
);
CREATE INDEX facts_article_idx ON facts (article_id);
CREATE INDEX facts_expires_idx ON facts (expires_at);

CREATE TABLE rundowns (
    id         BIGSERIAL PRIMARY KEY,
    block      TEXT NOT NULL,
    slot_start TIMESTAMPTZ NOT NULL,
    status     TEXT NOT NULL DEFAULT 'planned' CHECK (status IN ('planned', 'used', 'empty'))
);

-- Um item é um artigo (manchete) ou um fato avulso (mercado/clima).
CREATE TABLE rundown_items (
    rundown_id BIGINT NOT NULL REFERENCES rundowns(id) ON DELETE CASCADE,
    article_id BIGINT NULL REFERENCES articles(id),
    fact_id    BIGINT NULL REFERENCES facts(id),
    rank       INT NOT NULL,
    CHECK ((article_id IS NULL) <> (fact_id IS NULL))
);
CREATE INDEX rundown_items_rundown_idx ON rundown_items (rundown_id);

CREATE TABLE segments (
    id         BIGSERIAL PRIMARY KEY,
    rundown_id BIGINT NOT NULL REFERENCES rundowns(id),
    block      TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'approved', 'rejected')),
    attempts   INT NOT NULL DEFAULT 0,
    cost_usd   NUMERIC(12, 6) NOT NULL DEFAULT 0,
    reject_reason TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX segments_block_idx ON segments (block, created_at DESC);

CREATE TABLE lines (
    id            BIGSERIAL PRIMARY KEY,
    segment_id    BIGINT NOT NULL REFERENCES segments(id) ON DELETE CASCADE,
    seq           INT NOT NULL,
    speaker       TEXT NOT NULL,
    type          TEXT NOT NULL CHECK (type IN ('fact', 'banter')),
    text          TEXT NOT NULL,
    fact_ids      BIGINT[] NOT NULL DEFAULT '{}', -- como veio do roteiro (line_claims só guarda os existentes)
    original_text TEXT NULL, -- texto antes da reescrita
    status        TEXT NOT NULL DEFAULT 'ok' CHECK (status IN ('ok', 'rewritten', 'dropped')),
    reject_reason TEXT NULL,
    UNIQUE (segment_id, seq)
);

CREATE TABLE line_claims (
    line_id BIGINT NOT NULL REFERENCES lines(id) ON DELETE CASCADE,
    fact_id BIGINT NOT NULL REFERENCES facts(id),
    PRIMARY KEY (line_id, fact_id)
);

CREATE TABLE check_log (
    id         BIGSERIAL PRIMARY KEY,
    line_id    BIGINT NOT NULL REFERENCES lines(id) ON DELETE CASCADE,
    attempt    INT NOT NULL DEFAULT 1,
    stage      TEXT NOT NULL CHECK (stage IN ('deterministic', 'judge')),
    passed     BOOLEAN NOT NULL,
    detail     JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX check_log_line_idx ON check_log (line_id);

CREATE TABLE persona_memory (
    id                BIGSERIAL PRIMARY KEY,
    persona           TEXT NOT NULL,
    kind              TEXT NOT NULL CHECK (kind IN ('feud', 'joke', 'opinion', 'running_gag')),
    content           TEXT NOT NULL,
    source_segment_id BIGINT NULL REFERENCES segments(id),
    weight            DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE llm_calls (
    id            BIGSERIAL PRIMARY KEY,
    purpose       TEXT NOT NULL,
    model         TEXT NOT NULL,
    input_tokens  INT NOT NULL,
    output_tokens INT NOT NULL,
    cost_usd      NUMERIC(12, 6) NOT NULL,
    segment_id    BIGINT NULL REFERENCES segments(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX llm_calls_created_idx ON llm_calls (created_at);

-- Avisos operacionais (teto de orçamento, bloqueio eleitoral, fontes com falha).
CREATE TABLE system_events (
    id         BIGSERIAL PRIMARY KEY,
    kind       TEXT NOT NULL,
    detail     JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
