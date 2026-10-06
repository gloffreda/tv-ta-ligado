-- Sprint 3

-- Cache de prompt: tokens lidos/gravados no cache cobrados à parte.
ALTER TABLE llm_calls ADD COLUMN cache_read_tokens INT NOT NULL DEFAULT 0;
ALTER TABLE llm_calls ADD COLUMN cache_write_tokens INT NOT NULL DEFAULT 0;

-- Segmentos de dados (modelo determinístico, sem LLM) e fatos sensíveis.
ALTER TABLE segments ADD COLUMN origin TEXT NOT NULL DEFAULT 'llm' CHECK (origin IN ('llm', 'data'));
ALTER TABLE segments ADD COLUMN sensitive BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE facts ADD COLUMN sensitive BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE facts DROP CONSTRAINT facts_kind_check;
ALTER TABLE facts ADD CONSTRAINT facts_kind_check CHECK (kind IN ('headline', 'market', 'weather', 'glossary', 'alert'));
ALTER TABLE timeline DROP CONSTRAINT timeline_kind_check;
ALTER TABLE timeline ADD CONSTRAINT timeline_kind_check CHECK (kind IN ('segment', 'data', 'replay', 'bumper', 'silence'));
ALTER TABLE timeline DROP CONSTRAINT timeline_check1;
ALTER TABLE timeline ADD CONSTRAINT timeline_segment_check CHECK ((kind IN ('segment', 'data', 'replay')) = (segment_id IS NOT NULL));

-- Sessões sob demanda: o canal só roda dentro de uma sessão ligada à mão.
CREATE TABLE sessions (
    id          BIGSERIAL PRIMARY KEY,
    status      TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'ended')),
    started_at  TIMESTAMPTZ NOT NULL,
    deadline    TIMESTAMPTZ NOT NULL,
    max_usd     NUMERIC(10, 4) NOT NULL,
    ended_at    TIMESTAMPTZ NULL,
    end_reason  TEXT NULL,
    spent_usd   NUMERIC(10, 4) NOT NULL DEFAULT 0,
    origin_ip   TEXT NOT NULL,
    user_agent  TEXT NOT NULL DEFAULT '',
    first_line_at TIMESTAMPTZ NULL -- primeira fala no ar (tempo entre o clique e o ar)
);
-- No máximo uma sessão ativa.
CREATE UNIQUE INDEX sessions_one_active ON sessions ((status)) WHERE status = 'active';

-- Audiência: conexões SSE ativas, publicadas pela api.
CREATE TABLE audience (
    id          INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    viewers     INT NOT NULL DEFAULT 0,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NULL -- último momento com pelo menos 1 espectador
);
INSERT INTO audience (id) VALUES (1);

-- Memória: sobre quem (par de personagens), usos na semana e aposentadoria.
ALTER TABLE persona_memory ADD COLUMN about TEXT NULL;
ALTER TABLE persona_memory ADD COLUMN uses_week INT NOT NULL DEFAULT 0;
ALTER TABLE persona_memory ADD COLUMN week_start DATE NULL;
ALTER TABLE persona_memory ADD COLUMN retired_at TIMESTAMPTZ NULL;
CREATE TABLE persona_memory_log (
    id         BIGSERIAL PRIMARY KEY,
    action     TEXT NOT NULL, -- merged | retired | capped
    memory_ids BIGINT[] NOT NULL,
    detail     JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Humor do dia de cada personagem (0–10), volta devagar ao normal.
CREATE TABLE persona_mood (
    persona     TEXT PRIMARY KEY,
    irritacao   DOUBLE PRECISION NOT NULL DEFAULT 3,
    animo       DOUBLE PRECISION NOT NULL DEFAULT 6,
    rivalidade  DOUBLE PRECISION NOT NULL DEFAULT 5,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Tarefas periódicas que só rodam dentro de sessão (glossário, memória).
CREATE TABLE jobs (
    name     TEXT PRIMARY KEY,
    last_run TIMESTAMPTZ NOT NULL
);
