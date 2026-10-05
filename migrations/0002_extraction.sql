-- Extração de fatos passa a rodar na ingestão: marca quando cada artigo foi processado.
ALTER TABLE articles ADD COLUMN facts_extracted_at TIMESTAMPTZ NULL;
CREATE INDEX articles_pending_extraction_idx ON articles (published_at DESC) WHERE facts_extracted_at IS NULL;
