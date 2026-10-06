-- Sprint 2: voz e linha do tempo.

-- Texto para falar (normalizado). O texto checado (lines.text) nunca muda.
ALTER TABLE lines ADD COLUMN spoken_text TEXT NULL;

-- Áudio sintetizado, um arquivo por hash(voz + texto falado): mesma fala, mesmo arquivo.
CREATE TABLE audio_assets (
    hash        TEXT PRIMARY KEY,
    path        TEXT NOT NULL,
    duration_ms INT NOT NULL,
    provider    TEXT NOT NULL,
    voice       TEXT NOT NULL,
    spoken_text TEXT NOT NULL,
    visemes     JSONB NOT NULL DEFAULT '[]',
    cost_usd    NUMERIC(12, 6) NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE line_audio (
    line_id     BIGINT PRIMARY KEY REFERENCES lines(id) ON DELETE CASCADE,
    hash        TEXT NOT NULL REFERENCES audio_assets(hash),
    path        TEXT NOT NULL,
    duration_ms INT NOT NULL,
    provider    TEXT NOT NULL,
    voice       TEXT NOT NULL,
    visemes     JSONB NOT NULL DEFAULT '[]',
    cost_usd    NUMERIC(12, 6) NOT NULL DEFAULT 0, -- 0 quando veio do cache
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Linha do tempo única, em UTC. Itens agendados são imutáveis (gatilho abaixo).
CREATE TABLE timeline (
    id         BIGSERIAL PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('segment', 'replay', 'bumper', 'silence')),
    segment_id BIGINT NULL REFERENCES segments(id),
    block      TEXT NULL,
    starts_at  TIMESTAMPTZ NOT NULL,
    ends_at    TIMESTAMPTZ NOT NULL,
    status     TEXT NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled', 'aired', 'skipped')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at),
    CHECK ((kind IN ('segment', 'replay')) = (segment_id IS NOT NULL))
);
CREATE INDEX timeline_starts_idx ON timeline (starts_at);
CREATE INDEX timeline_ends_idx ON timeline (ends_at);
CREATE INDEX timeline_segment_idx ON timeline (segment_id, starts_at);

-- Falas de cada item, com o deslocamento já calculado (congelado no agendamento).
CREATE TABLE timeline_lines (
    timeline_id BIGINT NOT NULL REFERENCES timeline(id) ON DELETE CASCADE,
    seq         INT NOT NULL,
    line_id     BIGINT NULL REFERENCES lines(id),
    speaker     TEXT NOT NULL,
    type        TEXT NOT NULL,
    text        TEXT NOT NULL,
    spoken_text TEXT NOT NULL,
    audio_hash  TEXT NOT NULL REFERENCES audio_assets(hash),
    offset_ms   INT NOT NULL,
    duration_ms INT NOT NULL,
    PRIMARY KEY (timeline_id, seq)
);

CREATE FUNCTION timeline_immutable() RETURNS trigger AS $$
BEGIN
    IF NEW.starts_at <> OLD.starts_at OR NEW.ends_at <> OLD.ends_at OR NEW.kind <> OLD.kind
       OR NEW.segment_id IS DISTINCT FROM OLD.segment_id THEN
        RAISE EXCEPTION 'timeline: item % já agendado é imutável', OLD.id;
    END IF;
    RETURN NEW;
END $$ LANGUAGE plpgsql;
CREATE TRIGGER timeline_immutable BEFORE UPDATE ON timeline FOR EACH ROW EXECUTE FUNCTION timeline_immutable();

CREATE FUNCTION timeline_lines_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'timeline_lines: falas agendadas são imutáveis';
END $$ LANGUAGE plpgsql;
CREATE TRIGGER timeline_lines_immutable BEFORE UPDATE ON timeline_lines FOR EACH ROW EXECUTE FUNCTION timeline_lines_immutable();
