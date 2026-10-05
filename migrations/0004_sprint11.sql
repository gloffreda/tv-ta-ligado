-- Sprint 1.1
-- Entidades tipadas: ["Fulano"] vira [{"name":"Fulano","type":""}] (tipo desconhecido).
UPDATE facts SET entities = (
  SELECT COALESCE(jsonb_agg(CASE WHEN jsonb_typeof(e) = 'string'
                                 THEN jsonb_build_object('name', e #>> '{}', 'type', '')
                                 ELSE e END), '[]'::jsonb)
  FROM jsonb_array_elements(entities) e)
WHERE jsonb_typeof(entities) = 'array';

-- Glossário: fato sem validade (expires_at = 9999-12-31).
ALTER TABLE facts DROP CONSTRAINT facts_kind_check;
ALTER TABLE facts ADD CONSTRAINT facts_kind_check CHECK (kind IN ('headline', 'market', 'weather', 'glossary'));

-- Passe de continuidade: remove falas órfãs sem contar como corte da checagem.
ALTER TABLE lines DROP CONSTRAINT lines_status_check;
ALTER TABLE lines ADD CONSTRAINT lines_status_check CHECK (status IN ('ok', 'rewritten', 'dropped', 'removed'));
ALTER TABLE check_log DROP CONSTRAINT check_log_stage_check;
ALTER TABLE check_log ADD CONSTRAINT check_log_stage_check CHECK (stage IN ('deterministic', 'judge', 'continuity'));

-- Exibições: estreia (live) ou reprise (replay). A grade passa a olhar a última exibição.
CREATE TABLE airings (
    id         BIGSERIAL PRIMARY KEY,
    segment_id BIGINT NOT NULL REFERENCES segments(id),
    block      TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('live', 'replay')),
    aired_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX airings_block_idx ON airings (block, aired_at DESC);
INSERT INTO airings (segment_id, block, kind, aired_at)
SELECT id, block, 'live', created_at FROM segments WHERE status = 'approved';
